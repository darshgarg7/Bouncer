package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bouncer/internal/action"
	"bouncer/internal/eventlog"
)

func TestRunRecordsSuccessfulProviderRound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		candidate := action.Candidate{
			CandidateID:          "read",
			OperationClass:       "filesystem.read",
			Tool:                 "read_file",
			Target:               "workspace/service/config.yaml",
			Arguments:            map[string]any{},
			DeclaredDependencies: []string{},
			EstimatedObjectives:  action.Objectives{LatencyMS: 5, CostUnits: 0.01, SafetyRisk: 0.01},
		}
		content, err := json.Marshal(action.Beam{Actions: []action.Candidate{candidate}})
		if err != nil {
			t.Errorf("marshal response: %v", err)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(writer).Encode(map[string]any{
			"id": "harness-request", "model": "harness-model",
			"choices": []map[string]any{{
				"message": map[string]any{"content": string(content)}, "finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5,
			},
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	manifestData, err := os.ReadFile(filepath.Join(root, "configs/run-manifest.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["model"].(map[string]any)["endpoint"] = server.URL + "/v1"
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	eventPath := filepath.Join(t.TempDir(), "events.jsonl")
	if err := run(manifestPath, filepath.Join(root, "benchmarks/tasks/task-001.json"), eventPath); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	input, err := os.Open(eventPath)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	verification, err := eventlog.Verify(input)
	if err != nil {
		t.Fatal(err)
	}
	if verification.TerminalEvent != "run.completed" {
		t.Fatalf("unexpected verification: %+v", verification)
	}
}

func TestLoadTaskRejectsIncompleteAndInvalidDocuments(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"invalid JSON":   `{"task_id":`,
		"missing fields": `{"task_id":"task"}`,
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "task.json")
			if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadTask(path); err == nil {
				t.Fatal("loadTask returned nil error")
			}
		})
	}
	if _, err := loadTask(filepath.Join(t.TempDir(), "missing.json")); err == nil || !strings.Contains(err.Error(), "read task") {
		t.Fatalf("loadTask returned %v", err)
	}
}
