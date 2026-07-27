package executor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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

func TestValidateExecutionRequestEnforcesReadDenyAgain(t *testing.T) {
	state := benchmark.State{Files: map[string]string{}}
	policy := benchmark.Policy{
		AllowedOperationClasses: []string{"filesystem.read"},
		AllowedPathPrefixes:     []string{"workspace"},
		DeniedReadPaths:         []string{"workspace/private"},
	}
	candidate := action.Candidate{
		CandidateID:          "candidate-1",
		OperationClass:       "filesystem.read",
		Tool:                 "read_file",
		Target:               "workspace/private/token",
		Arguments:            map[string]any{},
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
	if err == nil || !strings.Contains(err.Error(), "denies reading") {
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

func TestNewRemoteAndExecutionTransportFailurePaths(t *testing.T) {
	for name, config := range map[string]executor.RemoteConfig{
		"invalid URL":    {BaseURL: ":"},
		"missing host":   {BaseURL: "https:///path", Token: "secret"},
		"insecure":       {BaseURL: "http://example.com", Token: "secret"},
		"authentication": {BaseURL: "https://example.com"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := executor.NewRemote(config); err == nil {
				t.Fatal("NewRemote accepted invalid configuration")
			}
		})
	}
	remote, err := executor.NewRemote(executor.RemoteConfig{
		BaseURL:              "https://example.com",
		AllowUnauthenticated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Execute(context.Background(), nil, benchmark.Policy{}, action.Candidate{}); err == nil {
		t.Fatal("remote accepted nil state")
	}

	failing, err := executor.NewRemote(executor.RemoteConfig{
		BaseURL:              "https://example.com",
		AllowUnauthenticated: true,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport failed")
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, policy, candidate := validRemoteInput()
	if _, err := failing.Execute(context.Background(), &state, policy, candidate); err == nil || !strings.Contains(err.Error(), "call sandbox") {
		t.Fatalf("transport failure returned %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type failingResponseBody struct{}

func (failingResponseBody) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failingResponseBody) Close() error             { return nil }

func TestRemoteRejectsMalformedOversizedAndMismatchedResponses(t *testing.T) {
	tests := map[string]struct {
		body       string
		status     int
		maxBytes   int64
		want       string
		readFailed bool
	}{
		"read":      {want: "read sandbox response", readFailed: true},
		"oversized": {body: strings.Repeat("x", 20), status: http.StatusOK, maxBytes: 8, want: "maximum size"},
		"HTTP":      {body: `{"error":"failed"}`, status: http.StatusBadGateway, want: "HTTP 502"},
		"decode":    {body: `{`, status: http.StatusOK, want: "decode sandbox response"},
		"trailing":  {body: `{} {}`, status: http.StatusOK, want: "trailing content"},
		"protocol":  {body: `{"schema_version":"old","idempotency_key":"wrong","state":{},"outcome":{}}`, status: http.StatusOK, want: "protocol or idempotency"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				body := io.NopCloser(bytes.NewBufferString(test.body))
				if test.readFailed {
					body = failingResponseBody{}
				}
				return &http.Response{StatusCode: test.status, Body: body, Header: make(http.Header)}, nil
			})}
			remote, err := executor.NewRemote(executor.RemoteConfig{
				BaseURL:              "https://example.com",
				AllowUnauthenticated: true,
				HTTPClient:           client,
				MaxResponseBytes:     test.maxBytes,
			})
			if err != nil {
				t.Fatal(err)
			}
			state, policy, candidate := validRemoteInput()
			if _, err := remote.Execute(context.Background(), &state, policy, candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Execute returned %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateExecutionRequestRejectsEveryPolicyBoundary(t *testing.T) {
	tests := map[string]func(*executor.ExecutionRequest){
		"protocol":  func(request *executor.ExecutionRequest) { request.SchemaVersion = "old" },
		"candidate": func(request *executor.ExecutionRequest) { request.Candidate.Tool = "" },
		"operation": func(request *executor.ExecutionRequest) {
			request.Policy.AllowedOperationClasses = []string{"filesystem.read"}
		},
		"target":  func(request *executor.ExecutionRequest) { request.Candidate.Target = "/absolute" },
		"outside": func(request *executor.ExecutionRequest) { request.Candidate.Target = "outside/file" },
		"mutation": func(request *executor.ExecutionRequest) {
			request.State.MutationCount = 1
			request.Policy.MaxMutations = 1
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			state, policy, candidate := validRemoteInput()
			request := executor.ExecutionRequest{
				SchemaVersion: "0.1.0",
				State:         state,
				Policy:        policy,
				Candidate:     candidate,
			}
			mutate(&request)
			key, err := executor.ComputeIdempotencyKey(request.State, request.Policy, request.Candidate)
			if err != nil {
				t.Fatal(err)
			}
			request.IdempotencyKey = key
			if err := executor.ValidateExecutionRequest(request); err == nil {
				t.Fatal("ValidateExecutionRequest accepted invalid boundary")
			}
		})
	}
	state, policy, candidate := validRemoteInput()
	candidate.Arguments["unsupported"] = make(chan int)
	if _, err := executor.ComputeIdempotencyKey(state, policy, candidate); err == nil {
		t.Fatal("idempotency key accepted unencodable input")
	}
}

func validRemoteInput() (benchmark.State, benchmark.Policy, action.Candidate) {
	return benchmark.State{Files: map[string]string{}, CompletedOperations: []string{}},
		benchmark.Policy{
			AllowedOperationClasses: []string{"filesystem.write"},
			AllowedPathPrefixes:     []string{"workspace"},
			MaxMutations:            1,
		},
		action.Candidate{
			CandidateID:          "candidate-1",
			OperationClass:       "filesystem.write",
			Tool:                 "write_file",
			Target:               "workspace/file",
			Arguments:            map[string]any{"content": "hello"},
			DeclaredDependencies: []string{},
			EstimatedObjectives:  action.Objectives{},
		}
}
