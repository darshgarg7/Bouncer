package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bouncer/internal/action"
	"bouncer/internal/nimclient"
)

func TestMockNIMCompletesOneHundredThreeCallBatches(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		response := map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"content": integrationBeamJSON()},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 20,
				"total_tokens":      30,
				"completion_tokens_details": map[string]any{
					"reasoning_tokens": 5,
				},
			},
		}
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client, err := nimclient.New(nimclient.Config{
		BaseURL:         server.URL,
		Model:           "mock-nemotron",
		ReasoningBudget: 1024,
		MaxTokens:       1536,
		Temperature:     0.7,
		MaxAttempts:     1,
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	coordinator := Coordinator{Proposer: client, ProposerCount: 3, Timeout: 2 * time.Second}
	for batch := 0; batch < 100; batch++ {
		results, err := coordinator.ProposeAll(context.Background(), Request{
			TaskID:      "task-001",
			Instruction: "mock batch",
			State:       json.RawMessage(`{"completed_operations":[],"files":{}}`),
			BaseSeed:    int64(batch * 3),
		})
		if err != nil {
			t.Fatalf("batch %d: %v", batch, err)
		}
		if len(results) != 3 {
			t.Fatalf("batch %d returned %d results", batch, len(results))
		}
		for _, result := range results {
			if result.FinishReason != "stop" || len(result.Beam.Actions) != action.BeamWidth {
				t.Fatalf("batch %d returned invalid result: %+v", batch, result)
			}
		}
	}
	if calls.Load() != 300 {
		t.Fatalf("got %d mock NIM calls, want 300", calls.Load())
	}
}

func integrationBeamJSON() string {
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
