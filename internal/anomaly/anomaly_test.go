package anomaly

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bouncer/internal/monitoring"
)

func TestRuntimeScoresDeterministically(t *testing.T) {
	runtime, err := New(testArtifact())
	if err != nil {
		t.Fatal(err)
	}
	normal, err := runtime.Score(monitoring.Features{})
	if err != nil {
		t.Fatal(err)
	}
	anomalous, err := runtime.Score(monitoring.Features{RejectionRate: 1})
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := runtime.Score(monitoring.Features{RejectionRate: 1})
	if err != nil {
		t.Fatal(err)
	}
	if anomalous != repeated {
		t.Fatalf("scoring is not deterministic: %+v %+v", anomalous, repeated)
	}
	if anomalous.Score <= normal.Score || normal.Alert || !anomalous.Alert {
		t.Fatalf("unexpected anomaly evaluations: normal=%+v anomalous=%+v", normal, anomalous)
	}
	if anomalous.Threshold != testArtifact().Threshold {
		t.Fatalf("threshold=%v", anomalous.Threshold)
	}
	var scorer Scorer = runtime
	if scorer.Metadata().ArtifactID != "test-anomaly" {
		t.Fatalf("unexpected scorer metadata: %+v", scorer.Metadata())
	}
}

func TestRuntimeMatchesCrossLanguageScoreFixture(t *testing.T) {
	data, err := os.ReadFile("../../examples/anomaly-score-fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Artifact Artifact `json:"artifact"`
		Cases    []struct {
			Name          string              `json:"name"`
			Features      monitoring.Features `json:"features"`
			ExpectedScore float64             `json:"expected_score"`
			ExpectedAlert bool                `json:"expected_alert"`
		} `json:"cases"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	runtime, err := New(fixture.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range fixture.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			evaluation, err := runtime.Score(testCase.Features)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(evaluation.Score-testCase.ExpectedScore) > 1e-15 ||
				evaluation.Alert != testCase.ExpectedAlert {
				t.Fatalf("evaluation=%+v expected_score=%.17g expected_alert=%t", evaluation, testCase.ExpectedScore, testCase.ExpectedAlert)
			}
		})
	}
}

func TestRuntimeAlertThresholdIsInclusive(t *testing.T) {
	artifact := testArtifact()
	baseline, err := New(artifact)
	if err != nil {
		t.Fatal(err)
	}
	features := monitoring.Features{RejectionRate: 1}
	evaluation, err := baseline.Score(features)
	if err != nil {
		t.Fatal(err)
	}
	artifact.Threshold = evaluation.Score
	atThreshold, err := New(artifact)
	if err != nil {
		t.Fatal(err)
	}
	equal, err := atThreshold.Score(features)
	if err != nil {
		t.Fatal(err)
	}
	if !equal.Alert || equal.Score != equal.Threshold {
		t.Fatalf("equal threshold must alert: %+v", equal)
	}
	artifact.Threshold = math.Nextafter(evaluation.Score, 1)
	aboveThreshold, err := New(artifact)
	if err != nil {
		t.Fatal(err)
	}
	below, err := aboveThreshold.Score(features)
	if err != nil {
		t.Fatal(err)
	}
	if below.Alert {
		t.Fatalf("score below threshold alerted: %+v", below)
	}
}

func TestRuntimeFreezesArtifactAndMetadata(t *testing.T) {
	artifact := testArtifact()
	runtime, err := New(artifact)
	if err != nil {
		t.Fatal(err)
	}
	artifact.Trees[0].Left.Size = 99
	artifact.Provenance.Validation.Rows = 99
	first := runtime.Metadata()
	first.Provenance.Validation.Rows = 100
	second := runtime.Metadata()
	if second.Provenance.Validation.Rows != 20 {
		t.Fatalf("runtime metadata was mutable: %+v", second)
	}
	if _, err := runtime.Score(monitoring.Features{}); err != nil {
		t.Fatalf("caller mutation changed frozen tree: %v", err)
	}
	var nilRuntime *Runtime
	if metadata := nilRuntime.Metadata(); metadata.ArtifactID != "" {
		t.Fatalf("nil metadata=%+v", metadata)
	}
}

func TestLoadUsesExactFileDigestAndStrictJSON(t *testing.T) {
	data, err := json.MarshalIndent(testArtifact(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	path := filepath.Join(t.TempDir(), "artifact.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if got, want := runtime.Metadata().ArtifactSHA256, hex.EncodeToString(digest[:]); got != want {
		t.Fatalf("digest=%q want %q", got, want)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing")); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("missing artifact error=%v", err)
	}
	for name, content := range map[string]string{
		"unknown": `{"schema_version":"0.1.0","unknown":true}`,
		"duplicate": strings.Replace(
			string(data),
			`"schema_version": "0.1.0"`,
			`"schema_version": "old", "schema_version": "0.1.0"`,
			1,
		),
		"trailing":  string(data) + `{}`,
		"malformed": `{`,
	} {
		t.Run(name, func(t *testing.T) {
			invalid := filepath.Join(t.TempDir(), name+".json")
			if err := os.WriteFile(invalid, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(invalid); err == nil {
				t.Fatal("Load accepted invalid JSON")
			}
		})
	}
	oversized := filepath.Join(t.TempDir(), "oversized.json")
	file, err := os.Create(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maximumArtifactBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(oversized); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized artifact error=%v", err)
	}
}

func TestCheckedInBootstrapArtifactLoadsShadowOnly(t *testing.T) {
	runtime, err := Load("../../configs/anomaly-artifact.bootstrap.json")
	if err != nil {
		t.Fatal(err)
	}
	metadata := runtime.Metadata()
	if metadata.ActiveEligible || metadata.Provenance.Validation != nil || metadata.ArtifactSHA256 == "" {
		t.Fatalf("bootstrap metadata=%+v", metadata)
	}
	if _, err := runtime.Score(monitoring.Features{}); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactValidationFailures(t *testing.T) {
	tests := map[string]func(*Artifact){
		"schema":         func(value *Artifact) { value.SchemaVersion = "old" },
		"feature schema": func(value *Artifact) { value.FeatureSchemaVersion = "old" },
		"empty ID":       func(value *Artifact) { value.ArtifactID = " " },
		"long ID":        func(value *Artifact) { value.ArtifactID = strings.Repeat("x", 129) },
		"created":        func(value *Artifact) { value.CreatedAt = time.Time{} },
		"feature count":  func(value *Artifact) { value.FeatureNames = value.FeatureNames[:5] },
		"feature order": func(value *Artifact) {
			value.FeatureNames[0], value.FeatureNames[1] = value.FeatureNames[1], value.FeatureNames[0]
		},
		"method":          func(value *Artifact) { value.Provenance.Method = " " },
		"dataset hash":    func(value *Artifact) { value.Provenance.DatasetSHA256 = "BAD" },
		"training rows":   func(value *Artifact) { value.Provenance.TrainingRows = 1 },
		"validation hash": func(value *Artifact) { value.Provenance.Validation.DatasetSHA256 = "BAD" },
		"reused validation": func(value *Artifact) {
			value.Provenance.Validation.DatasetSHA256 = value.Provenance.DatasetSHA256
		},
		"validation rows":    func(value *Artifact) { value.Provenance.Validation.Rows = 1 },
		"validation classes": func(value *Artifact) { value.Provenance.Validation.NormalRows = 0 },
		"validation total":   func(value *Artifact) { value.Provenance.Validation.Rows++ },
		"validation TPR":     func(value *Artifact) { value.Provenance.Validation.TruePositiveRate = math.NaN() },
		"validation FPR":     func(value *Artifact) { value.Provenance.Validation.FalsePositiveRate = 2 },
		"threshold zero":     func(value *Artifact) { value.Threshold = 0 },
		"threshold NaN":      func(value *Artifact) { value.Threshold = math.NaN() },
		"active unvalidated": func(value *Artifact) { value.Provenance.Validation = nil },
		"active weak holdout": func(value *Artifact) {
			value.Provenance.Validation.TruePositiveRate = minimumActiveTruePositiveRate - 0.01
		},
		"sample small":   func(value *Artifact) { value.SampleSize = 1 },
		"sample rows":    func(value *Artifact) { value.SampleSize = value.Provenance.TrainingRows + 1 },
		"no trees":       func(value *Artifact) { value.Trees = nil },
		"root size":      func(value *Artifact) { value.Trees[0].Size++ },
		"partial branch": func(value *Artifact) { value.Trees[0].Right = nil },
		"feature index":  func(value *Artifact) { *value.Trees[0].Feature = len(featureNames) },
		"split NaN":      func(value *Artifact) { *value.Trees[0].Split = math.NaN() },
		"child sizes":    func(value *Artifact) { value.Trees[0].Left.Size++ },
		"oversized child": func(value *Artifact) {
			value.Trees[0].Left.Size = math.MaxInt
		},
		"leaf size": func(value *Artifact) { value.Trees[0].Right.Size = 0 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			artifact := testArtifact()
			mutate(&artifact)
			if _, err := New(artifact); err == nil {
				t.Fatal("New accepted invalid artifact")
			}
		})
	}

	artifact := testArtifact()
	artifact.ActiveEligible = false
	artifact.Provenance.Validation = nil
	if _, err := New(artifact); err != nil {
		t.Fatalf("shadow-only artifact rejected: %v", err)
	}
}

func TestDuplicateFieldScannerHandlesEveryJSONContainer(t *testing.T) {
	for _, document := range []string{
		`[]`,
		`[1,{"nested":[true,null]}]`,
		`"scalar"`,
	} {
		if err := rejectDuplicateFields([]byte(document)); err != nil {
			t.Fatalf("rejectDuplicateFields(%s): %v", document, err)
		}
	}
	if err := rejectDuplicateFields([]byte(`{"outer":{"value":1,"value":2}}`)); err == nil {
		t.Fatal("nested duplicate field was accepted")
	}
}

func TestScoreRejectsInvalidFeatures(t *testing.T) {
	var nilRuntime *Runtime
	if _, err := nilRuntime.Score(monitoring.Features{}); err == nil {
		t.Fatal("nil runtime scored features")
	}
	runtime, err := New(testArtifact())
	if err != nil {
		t.Fatal(err)
	}
	tests := []monitoring.Features{
		{RejectionRate: -0.1},
		{RetryRate: 1.1},
		{NoProgressStreak: -1},
		{ToolSwitchRate: math.NaN()},
		{LatencyDeltaMS: math.Inf(1)},
		{TransitionNLL: -1},
	}
	for _, features := range tests {
		if _, err := runtime.Score(features); err == nil {
			t.Fatalf("Score accepted %+v", features)
		}
	}
}

func TestAveragePathLengthBoundaries(t *testing.T) {
	if averagePathLength(0) != 0 || averagePathLength(1) != 0 || averagePathLength(2) != 1 {
		t.Fatal("average path boundary changed")
	}
	if averagePathLength(3) <= 1 {
		t.Fatal("average path should grow with sample size")
	}
}

func testArtifact() Artifact {
	featureZero := 0
	featureTwo := 2
	rootSplit := 0.5
	streakSplit := 2.5
	return Artifact{
		SchemaVersion:        ArtifactSchemaVersion,
		ArtifactID:           "test-anomaly",
		FeatureSchemaVersion: FeatureSchemaVersion,
		FeatureNames:         FeatureNames(),
		CreatedAt:            time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
		Provenance: Provenance{
			Method:        "test",
			DatasetSHA256: strings.Repeat("0", 64),
			TrainingRows:  40,
			Seed:          42,
			Validation: &ValidationProvenance{
				DatasetSHA256:     strings.Repeat("1", 64),
				Rows:              20,
				NormalRows:        10,
				AnomalyRows:       10,
				TruePositiveRate:  0.9,
				FalsePositiveRate: 0.05,
			},
		},
		Threshold:      0.6,
		ActiveEligible: true,
		SampleSize:     4,
		Trees: []Node{{
			Size:    4,
			Feature: &featureZero,
			Split:   &rootSplit,
			Left: &Node{
				Size:    3,
				Feature: &featureTwo,
				Split:   &streakSplit,
				Left:    &Node{Size: 2},
				Right:   &Node{Size: 1},
			},
			Right: &Node{Size: 1},
		}},
	}
}

func FuzzArtifactValidationNeverPanics(f *testing.F) {
	valid, err := json.Marshal(testArtifact())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte(`{"schema_version":"0.1.0","unknown":true}`))
	f.Add([]byte("not-json"))
	f.Fuzz(func(t *testing.T, data []byte) {
		artifact, err := decodeStrict(data)
		if err == nil {
			_, _ = New(artifact)
		}
	})
}
