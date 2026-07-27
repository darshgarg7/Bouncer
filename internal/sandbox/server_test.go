package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
	"bouncer/internal/executor"
)

type countingExecutor struct {
	mutex sync.Mutex
	calls int
}

func (e *countingExecutor) Execute(
	ctx context.Context,
	state *benchmark.State,
	policy benchmark.Policy,
	candidate action.Candidate,
) (executor.Outcome, error) {
	e.mutex.Lock()
	e.calls++
	e.mutex.Unlock()
	return (executor.Virtual{}).Execute(ctx, state, policy, candidate)
}

func (e *countingExecutor) Calls() int {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	return e.calls
}

func TestDurableIdempotencySurvivesHandlerRestart(t *testing.T) {
	directory := t.TempDir()
	store, err := NewFileStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	backend := &countingExecutor{}
	metrics := NewMetrics()
	firstHandler, err := NewHandler(Config{Backend: backend, Store: store, Metrics: metrics})
	if err != nil {
		t.Fatal(err)
	}
	executionRequest := validExecutionRequest(t)
	first := performExecution(t, firstHandler, executionRequest)
	if first.Code != http.StatusOK || backend.Calls() != 1 {
		t.Fatalf("first response=%d calls=%d body=%s", first.Code, backend.Calls(), first.Body.String())
	}

	reopened, err := NewFileStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	secondHandler, err := NewHandler(Config{Backend: backend, Store: reopened, Metrics: metrics})
	if err != nil {
		t.Fatal(err)
	}
	second := performExecution(t, secondHandler, executionRequest)
	if second.Code != http.StatusOK || backend.Calls() != 1 {
		t.Fatalf("replayed response=%d calls=%d body=%s", second.Code, backend.Calls(), second.Body.String())
	}
	if second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("missing replay header: %v", second.Header())
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replayed response differs:\nfirst=%s\nsecond=%s", first.Body.String(), second.Body.String())
	}
	metricsRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsResponse := httptest.NewRecorder()
	secondHandler.ServeHTTP(metricsResponse, metricsRequest)
	for _, sample := range []string{
		"bouncer_sandbox_executions_total 1",
		"bouncer_sandbox_idempotency_replays_total 1",
	} {
		if !strings.Contains(metricsResponse.Body.String(), sample) {
			t.Fatalf("metrics do not contain %q:\n%s", sample, metricsResponse.Body.String())
		}
	}
}

func TestMemoryStoreRejectsKeyCollision(t *testing.T) {
	request := validExecutionRequest(t)
	response := executor.ExecutionResponse{
		SchemaVersion:  "0.1.0",
		IdempotencyKey: request.IdempotencyKey,
		State:          request.State,
	}
	store := NewMemoryStore()
	if claimed, err := store.Claim(context.Background(), request.IdempotencyKey); err != nil || !claimed {
		t.Fatalf("Claim returned claimed=%v error=%v", claimed, err)
	}
	if err := store.Put(context.Background(), request.IdempotencyKey, response); err != nil {
		t.Fatal(err)
	}
	response.State.TaskComplete = true
	if err := store.Put(context.Background(), request.IdempotencyKey, response); err == nil {
		t.Fatal("Put accepted different response for an existing key")
	}
}

func TestClaimWithoutCompletionFailsClosedAfterRestart(t *testing.T) {
	directory := t.TempDir()
	store, err := NewFileStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	executionRequest := validExecutionRequest(t)
	claimed, err := store.Claim(context.Background(), executionRequest.IdempotencyKey)
	if err != nil || !claimed {
		t.Fatalf("Claim returned claimed=%v error=%v", claimed, err)
	}
	reopened, err := NewFileStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	backend := &countingExecutor{}
	handler, err := NewHandler(Config{Backend: backend, Store: reopened})
	if err != nil {
		t.Fatal(err)
	}
	response := performExecution(t, handler, executionRequest)
	if response.Code != http.StatusConflict || backend.Calls() != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, backend.Calls(), response.Body.String())
	}
}

func TestConcurrentDuplicateRequestsExecuteBackendOnce(t *testing.T) {
	backend := &countingExecutor{}
	handler, err := NewHandler(Config{Backend: backend, Store: NewMemoryStore()})
	if err != nil {
		t.Fatal(err)
	}
	executionRequest := validExecutionRequest(t)
	const requests = 20
	statuses := make(chan int, requests)
	var waitGroup sync.WaitGroup
	for range requests {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			statuses <- performExecution(t, handler, executionRequest).Code
		}()
	}
	waitGroup.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("duplicate request returned status %d", status)
		}
	}
	if backend.Calls() != 1 {
		t.Fatalf("backend calls=%d, want one", backend.Calls())
	}
}

func validExecutionRequest(t *testing.T) executor.ExecutionRequest {
	t.Helper()
	state := benchmark.State{Files: map[string]string{}, CompletedOperations: []string{}}
	policy := benchmark.Policy{
		AllowedOperationClasses: []string{"filesystem.write"},
		AllowedPathPrefixes:     []string{"workspace"},
		MaxMutations:            1,
	}
	candidate := action.Candidate{
		CandidateID:          "candidate-1",
		OperationClass:       "filesystem.write",
		Tool:                 "write_file",
		Target:               "workspace/file.txt",
		Arguments:            map[string]any{"content": "hello"},
		DeclaredDependencies: []string{},
		EstimatedObjectives:  action.Objectives{LatencyMS: 1, CostUnits: 0.1, SafetyRisk: 0.1},
	}
	key, err := executor.ComputeIdempotencyKey(state, policy, candidate)
	if err != nil {
		t.Fatal(err)
	}
	return executor.ExecutionRequest{
		SchemaVersion:  "0.1.0",
		IdempotencyKey: key,
		State:          state,
		Policy:         policy,
		Candidate:      candidate,
	}
}

func performExecution(
	t *testing.T,
	handler http.Handler,
	executionRequest executor.ExecutionRequest,
) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(executionRequest)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/execute", bytes.NewReader(encoded))
	request.Header.Set("Idempotency-Key", executionRequest.IdempotencyKey)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
