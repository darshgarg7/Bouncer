package executor_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
	"bouncer/internal/executor"
	"bouncer/internal/sandbox"
)

func TestRemoteExecutesThroughAuthenticatedSandbox(t *testing.T) {
	handler, err := sandbox.NewHandler(sandbox.Config{
		Token:   "secret",
		Backend: executor.Virtual{},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	remote, err := executor.NewRemote(executor.RemoteConfig{
		BaseURL:           server.URL,
		Token:             "secret",
		AllowInsecureHTTP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := benchmark.State{Files: map[string]string{}, CompletedOperations: []string{}}
	candidate := action.Candidate{
		CandidateID:          "candidate-1",
		OperationClass:       "filesystem.write",
		Tool:                 "write_file",
		Target:               "workspace/file.txt",
		Arguments:            map[string]any{"content": "hello"},
		DeclaredDependencies: []string{},
		EstimatedObjectives:  action.Objectives{LatencyMS: 1, CostUnits: 0.1, SafetyRisk: 0.1},
	}
	outcome, err := remote.Execute(
		context.Background(),
		&state,
		benchmark.Policy{
			AllowedOperationClasses: []string{"filesystem.write"},
			AllowedPathPrefixes:     []string{"workspace"},
			MaxMutations:            1,
		},
		candidate,
	)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !outcome.Mutation || state.Files["workspace/file.txt"] != "hello" {
		t.Fatalf("unexpected remote state or outcome: state=%+v outcome=%+v", state, outcome)
	}
}

func TestRemoteRejectsTamperedStateTransition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var executionRequest executor.ExecutionRequest
		if err := json.NewDecoder(request.Body).Decode(&executionRequest); err != nil {
			t.Fatal(err)
		}
		state := executionRequest.State
		state.BenchmarkStep++
		state.CompletedOperations = []string{"filesystem.read"}
		state.Files["workspace/injected.txt"] = "tampered"
		_ = json.NewEncoder(writer).Encode(executor.ExecutionResponse{
			SchemaVersion:  "0.1.0",
			IdempotencyKey: executionRequest.IdempotencyKey,
			State:          state,
			Outcome: executor.Outcome{
				Diff: executor.StateDiff{CompletedOperation: "filesystem.read"},
			},
		})
	}))
	defer server.Close()
	remote, err := executor.NewRemote(executor.RemoteConfig{
		BaseURL:              server.URL,
		AllowUnauthenticated: true,
		AllowInsecureHTTP:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := benchmark.State{Files: map[string]string{}, CompletedOperations: []string{}}
	candidate := action.Candidate{
		CandidateID:          "candidate-1",
		OperationClass:       "filesystem.read",
		Tool:                 "read_file",
		Target:               "workspace/file.txt",
		Arguments:            map[string]any{},
		DeclaredDependencies: []string{},
		EstimatedObjectives:  action.Objectives{},
	}
	_, err = remote.Execute(
		context.Background(),
		&state,
		benchmark.Policy{
			AllowedOperationClasses: []string{"filesystem.read"},
			AllowedPathPrefixes:     []string{"workspace"},
		},
		candidate,
	)
	if err == nil || !strings.Contains(err.Error(), "deterministic transition contract") {
		t.Fatalf("got error %v", err)
	}
	if len(state.Files) != 0 || state.BenchmarkStep != 0 {
		t.Fatalf("tampered response mutated caller state: %+v", state)
	}
}

func TestValidateExecutionRequestEnforcesPolicyAgain(t *testing.T) {
	state := benchmark.State{Files: map[string]string{}}
	policy := benchmark.Policy{
		AllowedOperationClasses: []string{"filesystem.write"},
		AllowedPathPrefixes:     []string{"workspace"},
		ProtectedPaths:          []string{"workspace/protected"},
		MaxMutations:            1,
	}
	candidate := action.Candidate{
		CandidateID:          "candidate-1",
		OperationClass:       "filesystem.write",
		Tool:                 "write_file",
		Target:               "workspace/protected/secret.txt",
		Arguments:            map[string]any{"content": "tampered"},
		DeclaredDependencies: []string{},
		EstimatedObjectives:  action.Objectives{},
	}
	key, err := executor.ComputeIdempotencyKey(state, policy, candidate)
	if err != nil {
		t.Fatal(err)
	}
	err = executor.ValidateExecutionRequest(executor.ExecutionRequest{
		SchemaVersion:  "0.1.0",
		IdempotencyKey: key,
		State:          state,
		Policy:         policy,
		Candidate:      candidate,
	})
	if err == nil || !strings.Contains(err.Error(), "protects target") {
		t.Fatalf("got error %v", err)
	}
}

func TestRemoteFailsClosedOnTransportAndAuthentication(t *testing.T) {
	if _, err := executor.NewRemote(executor.RemoteConfig{BaseURL: "http://example.com"}); err == nil {
		t.Fatal("NewRemote accepted HTTP without explicit local override")
	}
	handler, err := sandbox.NewHandler(sandbox.Config{
		Token:   "secret",
		Backend: executor.Virtual{},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	remote, err := executor.NewRemote(executor.RemoteConfig{
		BaseURL:           server.URL,
		Token:             "wrong",
		AllowInsecureHTTP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := benchmark.State{Files: map[string]string{}}
	_, err = remote.Execute(
		context.Background(),
		&state,
		benchmark.Policy{},
		action.Candidate{
			CandidateID:          "candidate-1",
			OperationClass:       "task.complete",
			Tool:                 "complete",
			Target:               "task",
			Arguments:            map[string]any{},
			DeclaredDependencies: []string{},
			EstimatedObjectives:  action.Objectives{},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("got error %v", err)
	}
	if state.TaskComplete {
		t.Fatal("failed remote execution mutated caller state")
	}
}

func TestValidateExecutionRequestRejectsTamperedKey(t *testing.T) {
	request := executor.ExecutionRequest{
		SchemaVersion:  "0.1.0",
		IdempotencyKey: "wrong",
		State:          benchmark.State{Files: map[string]string{}},
		Policy:         benchmark.Policy{},
		Candidate: action.Candidate{
			CandidateID:          "candidate-1",
			OperationClass:       "task.complete",
			Tool:                 "complete",
			Target:               "task",
			Arguments:            map[string]any{},
			DeclaredDependencies: []string{},
			EstimatedObjectives:  action.Objectives{},
		},
	}
	if err := executor.ValidateExecutionRequest(request); err == nil {
		t.Fatal("ValidateExecutionRequest accepted a tampered idempotency key")
	}
}
