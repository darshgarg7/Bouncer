package learning

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
)

func TestRuntimeScoresPortableArtifactDeterministically(t *testing.T) {
	runtime, err := New(testArtifact())
	if err != nil {
		t.Fatal(err)
	}
	candidate := action.ScoredCandidate{
		Candidate: action.Candidate{
			CandidateID:          "candidate",
			OperationClass:       "filesystem.write",
			Tool:                 "write_file",
			Target:               "workspace/result.txt",
			Arguments:            map[string]any{"content": "ready"},
			DeclaredDependencies: []string{"filesystem.read"},
			EstimatedObjectives:  action.Objectives{LatencyMS: 10, CostUnits: 2, SafetyRisk: 0.1},
		},
		RoutingObjectives: action.Objectives{LatencyMS: 12, CostUnits: 3, SafetyRisk: 0.2},
	}
	decisionContext := Context{
		TaskID:   "task",
		Turn:     1,
		MaxTurns: 4,
		State: benchmark.State{
			CompletedOperations: []string{"filesystem.read"},
			Files:               map[string]string{"workspace/input.txt": "data"},
			MutationCount:       0,
		},
		Policy:            benchmark.Policy{MaxMutations: 2},
		PreviousOperation: "filesystem.read",
	}
	first, err := runtime.Score(context.Background(), decisionContext, []action.ScoredCandidate{candidate})
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Score(context.Background(), decisionContext, []action.ScoredCandidate{candidate})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Predictions) != 1 || len(second.Predictions) != 1 {
		t.Fatalf("unexpected prediction counts: %+v %+v", first, second)
	}
	left := first.Predictions[0]
	right := second.Predictions[0]
	if left.Progress != right.Progress || left.LatencyMS != right.LatencyMS {
		t.Fatalf("inference is not deterministic: %+v %+v", left, right)
	}
	if left.Progress.Conservative > left.Progress.Mean {
		t.Fatalf("progress did not use a lower confidence bound: %+v", left.Progress)
	}
	if left.LatencyMS.Conservative < left.LatencyMS.Mean {
		t.Fatalf("latency did not use an upper confidence bound: %+v", left.LatencyMS)
	}
	if left.Features["dependency_satisfaction_ratio"] != 1 ||
		left.Features["transition_unseen"] != 0 {
		t.Fatalf("unexpected trusted features: %+v", left.Features)
	}
}

func TestArtifactRejectsUnknownFeature(t *testing.T) {
	artifact := testArtifact()
	artifact.Models.Progress.Coefficients["provider_says_it_is_safe"] = 1
	if _, err := New(artifact); err == nil {
		t.Fatal("New accepted an unknown feature")
	}
}

func TestArtifactLoadMetadataAndValidationFailures(t *testing.T) {
	valid := testArtifact()
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "artifact.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Metadata().ArtifactID != valid.ArtifactID || runtime.Metadata().ArtifactSHA256 == "" {
		t.Fatalf("unexpected metadata: %+v", runtime.Metadata())
	}
	var nilRuntime *Runtime
	if metadata := nilRuntime.Metadata(); metadata != (Metadata{}) {
		t.Fatalf("nil runtime metadata=%+v", metadata)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing")); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("missing artifact returned %v", err)
	}
	malformed := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(malformed, []byte(`{"schema_version":"0.1.0"} {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(malformed); err == nil {
		t.Fatal("Load accepted malformed artifact")
	}

	tests := map[string]func(*Artifact){
		"schema":            func(value *Artifact) { value.SchemaVersion = "old" },
		"feature schema":    func(value *Artifact) { value.FeatureSchemaVersion = "old" },
		"empty ID":          func(value *Artifact) { value.ArtifactID = " " },
		"long ID":           func(value *Artifact) { value.ArtifactID = strings.Repeat("x", 129) },
		"created":           func(value *Artifact) { value.CreatedAt = time.Time{} },
		"method":            func(value *Artifact) { value.Provenance.Method = " " },
		"dataset hash":      func(value *Artifact) { value.Provenance.DatasetSHA256 = "BAD" },
		"training rows":     func(value *Artifact) { value.Provenance.TrainingRows = 0 },
		"validation rows":   func(value *Artifact) { value.Provenance.ValidationRows = -1 },
		"confidence NaN":    func(value *Artifact) { value.ConfidenceMultiplier = math.NaN() },
		"confidence high":   func(value *Artifact) { value.ConfidenceMultiplier = 11 },
		"model link":        func(value *Artifact) { value.Models.Progress.Link = "unknown" },
		"model intercept":   func(value *Artifact) { value.Models.Progress.Intercept = math.Inf(1) },
		"model uncertainty": func(value *Artifact) { value.Models.Progress.Uncertainty = -1 },
		"unknown feature":   func(value *Artifact) { value.Models.Progress.Coefficients["unknown"] = 1 },
		"coefficient NaN":   func(value *Artifact) { value.Models.Progress.Coefficients["candidate_mutating"] = math.NaN() },
		"required link":     func(value *Artifact) { value.Models.Success.Link = "identity" },
		"fallback":          func(value *Artifact) { value.TransitionPrior.FallbackProbability = 0 },
		"empty previous":    func(value *Artifact) { value.TransitionPrior.Probabilities[""] = map[string]float64{"read": 1} },
		"empty operation":   func(value *Artifact) { value.TransitionPrior.Probabilities["read"] = map[string]float64{"": 1} },
		"bad probability":   func(value *Artifact) { value.TransitionPrior.Probabilities["read"] = map[string]float64{"write": 2} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			artifact := testArtifact()
			mutate(&artifact)
			if _, err := New(artifact); err == nil {
				t.Fatal("New accepted malformed artifact")
			}
		})
	}
}

func TestScorerRejectsInvalidRuntimeContextAndCandidates(t *testing.T) {
	var nilRuntime *Runtime
	if _, err := nilRuntime.Score(context.Background(), Context{}, nil); err == nil {
		t.Fatal("nil runtime scored candidates")
	}
	runtime, err := New(testArtifact())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runtime.Score(ctx, Context{}, nil); err == nil {
		t.Fatal("canceled context was accepted")
	}
	if _, err := runtime.Score(context.Background(), Context{MaxTurns: 1}, nil); err == nil {
		t.Fatal("empty candidate set was accepted")
	}
	invalid := action.ScoredCandidate{Candidate: action.Candidate{CandidateID: "bad"}}
	if _, err := runtime.Score(context.Background(), Context{MaxTurns: 1}, []action.ScoredCandidate{invalid}); err == nil {
		t.Fatal("invalid candidate was accepted")
	}
	if _, err := runtime.Score(context.Background(), Context{Turn: -1, MaxTurns: 1}, []action.ScoredCandidate{invalid}); err == nil {
		t.Fatal("invalid turn horizon was accepted")
	}
	if inverseLink("identity", 2) != 2 || inverseLink("logit", 1000) != 1 {
		t.Fatal("inverse link boundary behavior changed")
	}
}

func TestFeatureSchemaIsUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for _, name := range FeatureNames() {
		if _, exists := seen[name]; exists {
			t.Fatalf("duplicate feature name %q", name)
		}
		seen[name] = struct{}{}
	}
}

func TestGoFeaturesMatchSharedFixture(t *testing.T) {
	data, err := os.ReadFile("../../examples/learning-feature-fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Context struct {
			Turn              int              `json:"turn"`
			MaxTurns          int              `json:"max_turns"`
			RecentRejections  int              `json:"recent_rejections"`
			NoProgressStreak  int              `json:"no_progress_streak"`
			PreviousOperation string           `json:"previous_operation"`
			State             benchmark.State  `json:"state"`
			Policy            benchmark.Policy `json:"policy"`
		} `json:"context"`
		Candidate       action.ScoredCandidate `json:"candidate"`
		TransitionPrior TransitionPrior        `json:"transition_prior"`
		Expected        map[string]float64     `json:"expected"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	actual := extractFeatures(Context{
		Turn:              fixture.Context.Turn,
		MaxTurns:          fixture.Context.MaxTurns,
		RecentRejections:  fixture.Context.RecentRejections,
		NoProgressStreak:  fixture.Context.NoProgressStreak,
		PreviousOperation: fixture.Context.PreviousOperation,
		State:             fixture.Context.State,
		Policy:            fixture.Context.Policy,
	}, fixture.Candidate, fixture.TransitionPrior)
	if len(actual) != len(fixture.Expected) {
		t.Fatalf("feature count %d, want %d", len(actual), len(fixture.Expected))
	}
	for name, expected := range fixture.Expected {
		if math.Abs(actual[name]-expected) > 1e-12 {
			t.Fatalf("feature %s = %.16g, want %.16g", name, actual[name], expected)
		}
	}
}

func testArtifact() Artifact {
	logit := LinearModel{
		Link:         "logit",
		Intercept:    -1,
		Coefficients: map[string]float64{"candidate_mutating": 0.5},
		Uncertainty:  0.05,
	}
	logModel := LinearModel{
		Link:         "log1p",
		Intercept:    math.Log1p(1),
		Coefficients: map[string]float64{"calibrated_latency_log1p": 0.5},
		Uncertainty:  0.5,
	}
	return Artifact{
		SchemaVersion:        ArtifactSchemaVersion,
		ArtifactID:           "test-artifact",
		FeatureSchemaVersion: FeatureSchemaVersion,
		CreatedAt:            time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
		Provenance: Provenance{
			Method:         "test",
			DatasetSHA256:  "0000000000000000000000000000000000000000000000000000000000000000",
			TrainingRows:   10,
			ValidationRows: 2,
		},
		ConfidenceMultiplier: 1.96,
		Models: Models{
			Progress:    logit,
			Success:     logit,
			LatencyMS:   logModel,
			CostUnits:   LinearModel{Link: "log1p", Coefficients: map[string]float64{}, Uncertainty: 0.1},
			AdverseRisk: logit,
		},
		TransitionPrior: TransitionPrior{
			FallbackProbability: 0.01,
			Probabilities: map[string]map[string]float64{
				"filesystem.read": {"filesystem.write": 0.8},
			},
		},
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
