package calibration

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bouncer/internal/action"
)

func TestBootstrapArtifactRemovesModelControl(t *testing.T) {
	runtime, err := Load("../../configs/objective-calibration.bootstrap.json")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	candidates := []action.Candidate{
		testCandidate("low-report", "filesystem.read", action.Objectives{
			LatencyMS: 1, CostUnits: 0.01, SafetyRisk: 0,
		}),
		testCandidate("high-report", "filesystem.read", action.Objectives{
			LatencyMS: 299999, CostUnits: 99, SafetyRisk: 1,
		}),
	}
	batch, err := runtime.Calibrate(candidates)
	if err != nil {
		t.Fatalf("Calibrate returned error: %v", err)
	}
	if len(batch.Candidates) != 2 || len(batch.Records) != 2 {
		t.Fatalf("unexpected batch size: %+v", batch)
	}
	first := batch.Candidates[0].RoutingObjectives
	second := batch.Candidates[1].RoutingObjectives
	if first != second {
		t.Fatalf("zero-influence artifact allowed self-reports to change routing: %+v != %+v", first, second)
	}
	if first != (action.Objectives{LatencyMS: 30, CostUnits: 0.05, SafetyRisk: 0.02}) {
		t.Fatalf("unexpected filesystem.read prior: %+v", first)
	}
	if batch.Records[0].RawObjectives == batch.Records[1].RawObjectives {
		t.Fatal("raw estimates were not preserved independently for audit")
	}
	if runtime.Metadata().ArtifactSHA256 == "" || runtime.Metadata().CalibrationID == "" {
		t.Fatalf("missing artifact identity: %+v", runtime.Metadata())
	}
}

func TestCalibrateBoundsTransformsAndBlends(t *testing.T) {
	artifact := testArtifact()
	artifact.InputBounds.LatencyMS.Maximum = 10
	artifact.InputBounds.CostUnits.Maximum = 5
	artifact.Transforms.LatencyMS = AffineTransform{Scale: 2, Offset: 1}
	artifact.Transforms.CostUnits = AffineTransform{Scale: 3, Offset: -2}
	artifact.Transforms.SafetyRisk = PlattTransform{Slope: 1, Intercept: 0}
	artifact.ModelInfluence = ModelInfluence{LatencyMS: 0.5, CostUnits: 0.5, SafetyRisk: 0.5}
	artifact.OperationPriors["filesystem.read"] = action.Objectives{
		LatencyMS: 1, CostUnits: 1, SafetyRisk: 0.4,
	}
	runtime, err := New(artifact)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	batch, err := runtime.Calibrate([]action.Candidate{testCandidate(
		"candidate",
		"filesystem.read",
		action.Objectives{LatencyMS: 100, CostUnits: 4, SafetyRisk: 0.2},
	)})
	if err != nil {
		t.Fatalf("Calibrate returned error: %v", err)
	}
	record := batch.Records[0]
	if record.BoundedObjectives.LatencyMS != 10 {
		t.Fatalf("latency was not bounded: %+v", record)
	}
	if record.TransformedObjectives.LatencyMS != 21 || record.TransformedObjectives.CostUnits != 10 {
		t.Fatalf("unexpected affine transform: %+v", record.TransformedObjectives)
	}
	if math.Abs(record.TransformedObjectives.SafetyRisk-0.2) > 1e-12 {
		t.Fatalf("unexpected Platt transform: %+v", record.TransformedObjectives)
	}
	if record.RoutingObjectives.LatencyMS != 11 ||
		record.RoutingObjectives.CostUnits != 5.5 ||
		math.Abs(record.RoutingObjectives.SafetyRisk-0.3) > 1e-12 {
		t.Fatalf("unexpected routing objectives: %+v", record.RoutingObjectives)
	}
}

func TestLoadRejectsUnknownAndTrailingContent(t *testing.T) {
	temporary := t.TempDir()
	base, err := os.ReadFile("../../configs/objective-calibration.bootstrap.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"unknown":  []byte(strings.Replace(string(base), `"calibration_id":`, `"unknown": true, "calibration_id":`, 1)),
		"trailing": append(append([]byte(nil), base...), []byte("\n{}")...),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(temporary, name+".json")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("Load returned nil error")
			}
		})
	}
}

func TestArtifactValidationFailsClosed(t *testing.T) {
	tests := map[string]func(*Artifact){
		"schema version": func(artifact *Artifact) { artifact.SchemaVersion = "9.9.9" },
		"missing id":     func(artifact *Artifact) { artifact.CalibrationID = " " },
		"missing method": func(artifact *Artifact) { artifact.Provenance.Method = "" },
		"invalid counts": func(artifact *Artifact) {
			artifact.Provenance = Provenance{Method: "test", SampleCount: 1, ValidationCount: 2}
		},
		"invalid dataset digest": func(artifact *Artifact) {
			artifact.Provenance.DatasetSHA256 = strings.Repeat("A", 64)
		},
		"missing fallback": func(artifact *Artifact) { delete(artifact.OperationPriors, "*") },
		"empty priors": func(artifact *Artifact) {
			artifact.OperationPriors = map[string]action.Objectives{}
		},
		"empty prior key": func(artifact *Artifact) {
			artifact.OperationPriors[""] = action.Objectives{}
		},
		"invalid prior": func(artifact *Artifact) {
			artifact.OperationPriors["*"] = action.Objectives{SafetyRisk: 2}
		},
		"bad influence": func(artifact *Artifact) {
			artifact.ModelInfluence.SafetyRisk = 1.1
		},
		"bad bound": func(artifact *Artifact) {
			artifact.InputBounds.SafetyRisk.Maximum = 2
		},
		"reversed bound": func(artifact *Artifact) {
			artifact.InputBounds.LatencyMS.Minimum = artifact.InputBounds.LatencyMS.Maximum
		},
		"negative affine scale": func(artifact *Artifact) {
			artifact.Transforms.LatencyMS.Scale = -1
		},
		"negative slope": func(artifact *Artifact) {
			artifact.Transforms.SafetyRisk.Slope = -1
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			artifact := testArtifact()
			mutate(&artifact)
			if _, err := New(artifact); err == nil {
				t.Fatal("New returned nil error")
			}
		})
	}
}

func TestCalibrateRejectsInvalidRuntimeInputs(t *testing.T) {
	var nilRuntime *Runtime
	if _, err := nilRuntime.Calibrate(nil); err == nil {
		t.Fatal("nil runtime returned nil error")
	}
	runtime, err := New(testArtifact())
	if err != nil {
		t.Fatal(err)
	}
	invalid := testCandidate("candidate", "filesystem.read", action.Objectives{SafetyRisk: 2})
	if _, err := runtime.Calibrate([]action.Candidate{invalid}); err == nil {
		t.Fatal("Calibrate accepted invalid raw objectives")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("Load accepted a missing artifact")
	}
}

func TestNewCopiesMutablePriorMap(t *testing.T) {
	artifact := testArtifact()
	runtime, err := New(artifact)
	if err != nil {
		t.Fatal(err)
	}
	artifact.OperationPriors["*"] = action.Objectives{
		LatencyMS: 999, CostUnits: 999, SafetyRisk: 1,
	}
	batch, err := runtime.Calibrate([]action.Candidate{testCandidate(
		"candidate",
		"unlisted.operation",
		action.Objectives{},
	)})
	if err != nil {
		t.Fatal(err)
	}
	if batch.Candidates[0].RoutingObjectives !=
		(action.Objectives{LatencyMS: 1, CostUnits: 1, SafetyRisk: 0.1}) {
		t.Fatalf("runtime changed after source map mutation: %+v", batch.Candidates[0])
	}
}

func testArtifact() Artifact {
	return Artifact{
		SchemaVersion: "0.1.0",
		CalibrationID: "calibration-test",
		Provenance:    Provenance{Method: "test_fixture"},
		InputBounds: ObjectiveBounds{
			LatencyMS:  Range{Minimum: 0, Maximum: 1000},
			CostUnits:  Range{Minimum: 0, Maximum: 100},
			SafetyRisk: Range{Minimum: 0, Maximum: 1},
		},
		Transforms: Transforms{
			LatencyMS:  AffineTransform{Scale: 1},
			CostUnits:  AffineTransform{Scale: 1},
			SafetyRisk: PlattTransform{Slope: 1},
		},
		OperationPriors: map[string]action.Objectives{
			"*": {LatencyMS: 1, CostUnits: 1, SafetyRisk: 0.1},
		},
	}
}

func testCandidate(id, operation string, objectives action.Objectives) action.Candidate {
	return action.Candidate{
		CandidateID:          id,
		OperationClass:       operation,
		Tool:                 "virtual",
		Target:               "workspace/file",
		Arguments:            map[string]any{},
		DeclaredDependencies: []string{},
		EstimatedObjectives:  objectives,
	}
}
