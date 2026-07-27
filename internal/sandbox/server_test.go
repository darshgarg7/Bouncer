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

func TestHandlerEnforcesRequestSizeAndRateLimits(t *testing.T) {
	handler, err := NewHandler(Config{
		Backend:           &countingExecutor{},
		MaxBodyBytes:      8,
		RequestsPerSecond: 0.0001,
		Burst:             1,
	})
	if err != nil {
		t.Fatal(err)
	}
	oversized := httptest.NewRequest(http.MethodPost, "/v1/execute", strings.NewReader("0123456789"))
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, oversized)
	if first.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized request returned %d", first.Code)
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, oversized)
	if second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") != "1" {
		t.Fatalf("rate-limited request returned %d headers=%v", second.Code, second.Header())
	}
}

func TestHandlerRejectsInvalidRoutesMethodsAuthenticationAndBodies(t *testing.T) {
	if _, err := NewHandler(Config{}); err == nil {
		t.Fatal("handler accepted missing backend")
	}
	handler, err := NewHandler(Config{Backend: &countingExecutor{}, Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"health", http.MethodGet, "/health/ready", http.StatusOK},
		{"health method", http.MethodPost, "/health/live", http.StatusMethodNotAllowed},
		{"metrics method", http.MethodPost, "/metrics", http.StatusMethodNotAllowed},
		{"unknown", http.MethodGet, "/unknown", http.StatusNotFound},
		{"execute method", http.MethodGet, "/v1/execute", http.StatusMethodNotAllowed},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d", response.Code, test.want)
			}
		})
	}
	unauthorized := httptest.NewRequest(http.MethodPost, "/v1/execute", strings.NewReader(`{}`))
	unauthorized.Header.Set("Authorization", "Bearer wrong")
	unauthorizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorizedResponse.Code)
	}

	for name, body := range map[string]string{
		"malformed": "{",
		"trailing":  `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/execute", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
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

func FuzzIdempotencyCollisionRejected(f *testing.F) {
	f.Add("first", "second")
	f.Add("same", "same")
	f.Fuzz(func(t *testing.T, firstContent, secondContent string) {
		const key = "0000000000000000000000000000000000000000000000000000000000000000"
		store := NewMemoryStore()
		if claimed, err := store.Claim(context.Background(), key); err != nil || !claimed {
			t.Fatalf("initial claim: claimed=%v error=%v", claimed, err)
		}
		first := executor.ExecutionResponse{
			SchemaVersion:  "0.1.0",
			IdempotencyKey: key,
			State: benchmark.State{
				Files:               map[string]string{"workspace/file": firstContent},
				CompletedOperations: []string{},
			},
		}
		if err := store.Put(context.Background(), key, first); err != nil {
			t.Fatal(err)
		}
		second := first
		second.State.Files = map[string]string{"workspace/file": secondContent}
		err := store.Put(context.Background(), key, second)
		if firstContent == secondContent && err != nil {
			t.Fatalf("identical replay rejected: %v", err)
		}
		if firstContent != secondContent && err == nil {
			t.Fatal("different response accepted for an existing idempotency key")
		}
	})
}
