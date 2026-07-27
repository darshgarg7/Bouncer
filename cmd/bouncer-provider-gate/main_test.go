package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"bouncer/internal/action"
	"bouncer/internal/providergate"
)

func TestRunWritesImmutablePassingGateArtifacts(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
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
			"id":    "gate-request",
			"model": "gate-model",
			"choices": []map[string]any{{
				"message":       map[string]any{"content": string(content)},
				"finish_reason": "stop",
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
	outputDir := filepath.Join(t.TempDir(), "gate")
	err = run(
		filepath.Join(root, "configs/run-manifest.example.json"),
		filepath.Join(root, "benchmarks/tasks/task-001.json"),
		server.URL+"/v1",
		outputDir,
		2,
		99,
	)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("provider received %d requests, want 2", requests.Load())
	}
	data, err := os.ReadFile(filepath.Join(outputDir, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var summary providergate.Summary
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	if !summary.Passed || summary.CompletedBatches != 2 || summary.ManifestSHA256 == "" || summary.TaskSHA256 == "" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "batches.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := run(
		filepath.Join(root, "configs/run-manifest.example.json"),
		filepath.Join(root, "benchmarks/tasks/task-001.json"),
		server.URL+"/v1",
		outputDir,
		1,
		99,
	); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second run returned %v", err)
	}
}

func TestProviderGateHelpersRejectUnsafeReuse(t *testing.T) {
	t.Parallel()
	temporary := t.TempDir()
	path := filepath.Join(temporary, "artifact.json")
	if err := writeJSONExclusive(path, map[string]bool{"passed": true}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONExclusive(path, map[string]bool{"passed": false}); err == nil {
		t.Fatal("writeJSONExclusive overwrote an artifact")
	}
	if digest, err := fileSHA256(path); err != nil || len(digest) != 64 {
		t.Fatalf("fileSHA256 returned %q, %v", digest, err)
	}
	if _, err := fileSHA256(filepath.Join(temporary, "missing")); err == nil {
		t.Fatal("fileSHA256 accepted a missing file")
	}
}
