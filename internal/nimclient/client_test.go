package nimclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bouncer/internal/action"
)

func TestProposeAcceptsStopWithExactlyFiveActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Fatalf("got path %s, want /v1/chat/completions", request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := int(body["max_tokens"].(float64)); got != 1536 {
			t.Fatalf("got max_tokens %d, want 1536", got)
		}
		if got := int(body["thinking_token_budget"].(float64)); got != 1024 {
			t.Fatalf("got thinking_token_budget %d, want 1024", got)
		}
		messages := body["messages"].([]any)
		user := messages[1].(map[string]any)["content"].(string)
		if !strings.Contains(user, "Declared policy JSON") || !strings.Contains(user, "allowed_path_prefixes") {
			t.Fatalf("proposal prompt omitted declared policy: %s", user)
		}
		writeResponse(t, writer, "stop", testBeamJSON(), 17)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL+"/v1", 1, nil)
	result, err := client.Propose(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Propose returned error: %v", err)
	}
	if result.FinishReason != "stop" || len(result.Beam.Actions) != action.BeamWidth {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Usage.ReasoningTokens != 17 {
		t.Fatalf("got %d reasoning tokens, want 17", result.Usage.ReasoningTokens)
	}
	if len(result.RequestHash) != 64 || len(result.ResponseHash) != 64 ||
		result.ProviderRequestID != "request-1" || result.Model != "test-model" {
		t.Fatalf("missing provider evidence: %+v", result)
	}
}

func TestProposeRequiresTypedPolicy(t *testing.T) {
	client := newTestClient(t, "http://localhost", 1, nil)
	request := testRequest()
	request.Policy = nil
	if _, err := client.Propose(context.Background(), request); err == nil || !strings.Contains(err.Error(), "state and policy") {
		t.Fatalf("missing policy returned %v", err)
	}
}

func TestProposeUsesHostedReasoningBudgetDialectExclusively(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if _, exists := body["thinking_token_budget"]; exists {
			t.Fatal("hosted request included thinking_token_budget")
		}
		if got := int(body["reasoning_budget"].(float64)); got != 1024 {
			t.Fatalf("got reasoning_budget %d, want 1024", got)
		}
		if got := body["top_p"].(float64); got != 0.95 {
			t.Fatalf("got top_p %v, want 0.95", got)
		}
		if got := body["reasoning_effort"].(string); got != "medium" {
			t.Fatalf("got reasoning_effort %q, want medium", got)
		}
		kwargs := body["chat_template_kwargs"].(map[string]any)
		if enabled, ok := kwargs["enable_thinking"].(bool); !ok || !enabled {
			t.Fatalf("unexpected chat_template_kwargs: %+v", kwargs)
		}
		writeResponse(t, writer, "stop", testBeamJSON(), 17)
	}))
	defer server.Close()

	topP := 0.95
	client, err := New(Config{
		BaseURL:         server.URL,
		Model:           "test-model",
		ReasoningBudget: 1024,
		BudgetParameter: "reasoning_budget",
		MaxTokens:       1536,
		TopP:            &topP,
		ReasoningEffort: "medium",
		MaxAttempts:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Propose(context.Background(), testRequest()); err != nil {
		t.Fatalf("Propose returned error: %v", err)
	}
}

func TestAPIKeyFromEnvironmentPrefersGenericNameAndSupportsNVIDIAAlias(t *testing.T) {
	t.Setenv("NIM_API_KEY", "")
	t.Setenv("NVIDIA_API_KEY", "nvidia-key")
	if got := APIKeyFromEnvironment(); got != "nvidia-key" {
		t.Fatalf("got key %q from NVIDIA alias", got)
	}
	t.Setenv("NIM_API_KEY", "generic-key")
	if got := APIKeyFromEnvironment(); got != "generic-key" {
		t.Fatalf("got key %q, want generic key precedence", got)
	}
}

func TestNewRejectsUnknownBudgetDialect(t *testing.T) {
	_, err := New(Config{
		BaseURL:         "http://localhost/v1",
		Model:           "test-model",
		ReasoningBudget: 1,
		BudgetParameter: "unknown",
		MaxTokens:       2,
		MaxAttempts:     1,
	})
	if err == nil || !strings.Contains(err.Error(), "budget parameter") {
		t.Fatalf("got error %v", err)
	}
}

func TestProposeRejectsLengthEvenWhenContentIsValidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeResponse(t, writer, "length", testBeamJSON(), 1024)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 1, nil)
	_, err := client.Propose(context.Background(), testRequest())
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("got error %v, want ErrTruncated", err)
	}
}

func TestProposeRejectsUnexpectedFinishReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeResponse(t, writer, "tool_calls", testBeamJSON(), 3)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 1, nil)
	_, err := client.Propose(context.Background(), testRequest())
	if !errors.Is(err, ErrUnexpectedFinishReason) {
		t.Fatalf("got error %v, want ErrUnexpectedFinishReason", err)
	}
}

func TestProposeRejectsMarkdownWrappedBeam(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeResponse(t, writer, "stop", "```json\n"+testBeamJSON()+"\n```", 3)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 1, nil)
	if _, err := client.Propose(context.Background(), testRequest()); err == nil {
		t.Fatal("Propose accepted Markdown-wrapped content")
	}
}

func TestProposeRetries429AndServerErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		switch calls.Add(1) {
		case 1:
			writer.Header().Set("Retry-After", "0")
			http.Error(writer, "rate limited", http.StatusTooManyRequests)
		case 2:
			http.Error(writer, "temporary", http.StatusServiceUnavailable)
		default:
			writeResponse(t, writer, "stop", testBeamJSON(), 8)
		}
	}))
	defer server.Close()

	var sleeps atomic.Int32
	client := newTestClient(t, server.URL, 3, func(context.Context, time.Duration) error {
		sleeps.Add(1)
		return nil
	})
	result, err := client.Propose(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Propose returned error: %v", err)
	}
	if calls.Load() != 3 || sleeps.Load() != 2 || result.Attempts != 3 {
		t.Fatalf("calls=%d sleeps=%d attempts=%d, want 3, 2, 3", calls.Load(), sleeps.Load(), result.Attempts)
	}
}

func TestProposeDoesNotRetryBadRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(writer, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 3, nil)
	if _, err := client.Propose(context.Background(), testRequest()); err == nil {
		t.Fatal("Propose returned nil error for HTTP 400")
	}
	if calls.Load() != 1 {
		t.Fatalf("got %d calls, want 1", calls.Load())
	}
}

func newTestClient(t *testing.T, baseURL string, attempts int, sleep func(context.Context, time.Duration) error) *Client {
	t.Helper()
	client, err := New(Config{
		BaseURL:         baseURL,
		Model:           "test-model",
		ReasoningBudget: 1024,
		MaxTokens:       1536,
		Temperature:     0.7,
		MaxAttempts:     attempts,
		BaseDelay:       time.Millisecond,
		MaxDelay:        time.Millisecond,
		Sleep:           sleep,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return client
}

func testRequest() ProposalRequest {
	return ProposalRequest{
		TaskID:      "task-001",
		Instruction: "Inspect the file.",
		State:       json.RawMessage(`{"completed_operations":[],"files":{}}`),
		Policy:      json.RawMessage(`{"allowed_operation_classes":["filesystem.read"],"allowed_path_prefixes":["workspace/"]}`),
		ProposerID:  "agent-1",
		Seed:        42,
	}
}

func writeResponse(t *testing.T, writer http.ResponseWriter, finishReason, content string, reasoningTokens int) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	response := map[string]any{
		"id":    "request-1",
		"model": "test-model",
		"choices": []any{map[string]any{
			"message":       map[string]any{"content": content},
			"finish_reason": finishReason,
		}},
		"usage": map[string]any{
			"prompt_tokens":     20,
			"completion_tokens": 30,
			"total_tokens":      50,
			"completion_tokens_details": map[string]any{
				"reasoning_tokens": reasoningTokens,
			},
		},
	}
	if err := json.NewEncoder(writer).Encode(response); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func testBeamJSON() string {
	var builder strings.Builder
	builder.WriteString(`{"actions":[`)
	for index := 1; index <= action.BeamWidth; index++ {
		if index > 1 {
			builder.WriteByte(',')
		}
		builder.WriteString(fmt.Sprintf(
			`{"candidate_id":"candidate-%d","operation_class":"filesystem.read","tool":"read_file","target":"workspace/file-%d.txt","arguments":{},"declared_dependencies":[],"estimated_objectives":{"latency_ms":%d,"cost_units":0.01,"safety_risk":0.01}}`,
			index,
			index,
			index,
		))
	}
	builder.WriteString(`]}`)
	return builder.String()
}
