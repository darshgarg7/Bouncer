package executor

import (
	"context"
	"strings"
	"testing"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
)

func TestVirtualWriteCreatesFileAndTracksMutation(t *testing.T) {
	state := benchmark.State{Files: map[string]string{}, CompletedOperations: []string{}}
	outcome, err := (Virtual{}).Execute(context.Background(), &state, benchmark.Policy{MaxMutations: 1}, action.Candidate{
		CandidateID:    "candidate",
		OperationClass: "filesystem.write",
		Target:         "workspace/file.txt",
		Arguments:      map[string]any{"content": "hello\n"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !outcome.Mutation || state.Files["workspace/file.txt"] != "hello\n" || state.MutationCount != 1 {
		t.Fatalf("unexpected state or outcome: state=%+v outcome=%+v", state, outcome)
	}
}

func TestVirtualRejectsMutationAfterLimit(t *testing.T) {
	state := benchmark.State{Files: map[string]string{}, MutationCount: 1}
	_, err := (Virtual{}).Execute(context.Background(), &state, benchmark.Policy{MaxMutations: 1}, action.Candidate{
		OperationClass: "filesystem.write",
		Target:         "workspace/file.txt",
		Arguments:      map[string]any{"content": "hello\n"},
	})
	if err == nil {
		t.Fatal("Execute returned nil after mutation limit")
	}
}

func TestVirtualRejectsNilState(t *testing.T) {
	_, err := (Virtual{}).Execute(context.Background(), nil, benchmark.Policy{}, action.Candidate{})
	if err == nil || !strings.Contains(err.Error(), "state is required") {
		t.Fatalf("got error %v", err)
	}
}

func TestVirtualExecutesNonMutatingOperationsAndDeduplicatesHistory(t *testing.T) {
	operations := []string{"filesystem.read", "state.validate", "state.backup", "command.run"}
	state := benchmark.State{
		Files:               map[string]string{},
		CompletedOperations: []string{"filesystem.read"},
		ConstraintFeedback:  []string{"old feedback"},
	}
	for _, operation := range operations {
		outcome, err := (Virtual{}).Execute(context.Background(), &state, benchmark.Policy{}, action.Candidate{OperationClass: operation})
		if err != nil {
			t.Fatalf("%s returned error: %v", operation, err)
		}
		if outcome.Mutation || outcome.Diff.CompletedOperation != operation {
			t.Fatalf("unexpected %s outcome: %+v", operation, outcome)
		}
	}
	if len(state.CompletedOperations) != len(operations) {
		t.Fatalf("operation history was not deduplicated: %v", state.CompletedOperations)
	}
	if state.BenchmarkStep != len(operations) || state.ConstraintFeedback != nil {
		t.Fatalf("state bookkeeping failed: %+v", state)
	}
}

func TestVirtualWriteModifiesExistingFile(t *testing.T) {
	state := benchmark.State{Files: map[string]string{"workspace/file.txt": "old"}}
	outcome, err := (Virtual{}).Execute(context.Background(), &state, benchmark.Policy{MaxMutations: 1}, action.Candidate{
		OperationClass: "filesystem.write",
		Target:         "workspace/file.txt",
		Arguments:      map[string]any{"content": "new"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Diff.Modified) != 1 || len(outcome.Diff.Created) != 0 {
		t.Fatalf("unexpected write diff: %+v", outcome.Diff)
	}
}

func TestVirtualDeleteHandlesPresentAndAbsentTargets(t *testing.T) {
	state := benchmark.State{Files: map[string]string{"workspace/file.txt": "content"}}
	outcome, err := (Virtual{}).Execute(context.Background(), &state, benchmark.Policy{MaxMutations: 2}, action.Candidate{
		OperationClass: "filesystem.delete",
		Target:         "workspace/file.txt",
		Arguments:      map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Diff.Deleted) != 1 || outcome.Diff.Deleted[0] != "workspace/file.txt" {
		t.Fatalf("unexpected delete diff: %+v", outcome.Diff)
	}
	_, err = (Virtual{}).Execute(context.Background(), &state, benchmark.Policy{MaxMutations: 2}, action.Candidate{
		OperationClass: "filesystem.delete",
		Target:         "workspace/file.txt",
		Arguments:      map[string]any{},
	})
	if err == nil || !strings.Contains(err.Error(), "absent path") {
		t.Fatalf("got absent-delete error %v", err)
	}
}

func TestVirtualDeployCreatesArtifact(t *testing.T) {
	state := benchmark.State{Files: map[string]string{}}
	outcome, err := (Virtual{}).Execute(context.Background(), &state, benchmark.Policy{MaxMutations: 1}, action.Candidate{
		OperationClass: "service.deploy",
		Target:         "workspace/deployment.json",
		Arguments:      map[string]any{"content": "deployed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Files["workspace/deployment.json"] != "deployed" || len(outcome.Diff.Created) != 1 {
		t.Fatalf("unexpected deploy result: state=%+v outcome=%+v", state, outcome)
	}
}

func TestVirtualCompletesTask(t *testing.T) {
	state := benchmark.State{Files: map[string]string{}}
	outcome, err := (Virtual{}).Execute(context.Background(), &state, benchmark.Policy{}, action.Candidate{OperationClass: "task.complete"})
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Complete || !state.TaskComplete {
		t.Fatalf("task did not complete: state=%+v outcome=%+v", state, outcome)
	}
}

func TestVirtualRejectsMalformedAndUnsupportedActions(t *testing.T) {
	tests := []struct {
		name      string
		candidate action.Candidate
		want      string
	}{
		{
			name:      "write content",
			candidate: action.Candidate{OperationClass: "filesystem.write", Arguments: map[string]any{}},
			want:      "requires string",
		},
		{
			name:      "deploy content",
			candidate: action.Candidate{OperationClass: "service.deploy", Arguments: map[string]any{}},
			want:      "requires string",
		},
		{
			name:      "unsupported",
			candidate: action.Candidate{OperationClass: "network.open", Arguments: map[string]any{}},
			want:      "unsupported operation",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := benchmark.State{Files: map[string]string{}}
			_, err := (Virtual{}).Execute(context.Background(), &state, benchmark.Policy{MaxMutations: 1}, test.candidate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got error %v", err)
			}
		})
	}
}
