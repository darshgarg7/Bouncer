package learning

import (
	"context"
	"encoding/json"
	"math"
	"os"
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
