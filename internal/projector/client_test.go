package projector

import (
	"context"
	"testing"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
)

func TestPythonClientEvaluatesBatch(t *testing.T) {
	client := PythonClient{WorkingDir: "../.."}
	actions := []action.Candidate{
		testCandidate("allowed", "workspace/service/config.yaml"),
		testCandidate("outside", "workspace/other/config.yaml"),
	}
	results, err := client.Evaluate(
		context.Background(),
		actions,
		benchmark.State{CompletedOperations: []string{"filesystem.read"}, Files: map[string]string{}},
		benchmark.Policy{
			AllowedOperationClasses: []string{"filesystem.write"},
			AllowedPathPrefixes:     []string{"workspace/service/"},
			ProtectedPaths:          []string{},
			MaxMutations:            1,
		},
	)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if len(results) != 2 || !results[0].Allowed || results[1].Allowed {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestPersistentClientReusesProcessAcrossBatches(t *testing.T) {
	client, err := NewPersistentClient(PythonClient{WorkingDir: "../.."})
	if err != nil {
		t.Fatalf("NewPersistentClient returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close returned error: %v", err)
		}
	})
	actions := []action.Candidate{testCandidate("allowed", "workspace/service/config.yaml")}
	state := benchmark.State{
		CompletedOperations: []string{"filesystem.read"},
		Files:               map[string]string{},
	}
	policy := benchmark.Policy{
		AllowedOperationClasses: []string{"filesystem.write"},
		AllowedPathPrefixes:     []string{"workspace/service/"},
		ProtectedPaths:          []string{},
		MaxMutations:            1,
	}
	for batch := 0; batch < 2; batch++ {
		results, err := client.Evaluate(context.Background(), actions, state, policy)
		if err != nil {
			t.Fatalf("Evaluate batch %d returned error: %v", batch, err)
		}
		if len(results) != 1 || !results[0].Allowed {
			t.Fatalf("unexpected batch %d results: %+v", batch, results)
		}
	}
}

func TestPersistentClientRejectsUseAfterClose(t *testing.T) {
	client, err := NewPersistentClient(PythonClient{WorkingDir: "../.."})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = client.Evaluate(
		context.Background(),
		[]action.Candidate{testCandidate("allowed", "workspace/service/config.yaml")},
		benchmark.State{},
		benchmark.Policy{},
	)
	if err == nil {
		t.Fatal("Evaluate succeeded after Close")
	}
}

func testCandidate(id, target string) action.Candidate {
	return action.Candidate{
		CandidateID:          id,
		OperationClass:       "filesystem.write",
		Tool:                 "apply_patch",
		Target:               target,
		Arguments:            map[string]any{"content": "value\n"},
		DeclaredDependencies: []string{"filesystem.read"},
		EstimatedObjectives:  action.Objectives{LatencyMS: 1, CostUnits: 1, SafetyRisk: 0.1},
	}
}
