package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"bouncer/internal/action"
	"bouncer/internal/control"
	"bouncer/internal/eventlog"
)

func TestRunCompletesTaskThroughHTTPProvider(t *testing.T) {
	t.Parallel()
	actions := []action.Candidate{
		commandCandidate("read", "filesystem.read", "read_file", map[string]any{}, nil),
		commandCandidate("write", "filesystem.write", "apply_patch", map[string]any{
			"content": "timeout: 60\nretries: 3\n",
		}, []string{"filesystem.read"}),
		commandCandidate("validate", "state.validate", "validate_yaml", map[string]any{}, []string{"filesystem.read"}),
		commandCandidate("complete", "task.complete", "complete_task", map[string]any{}, []string{"state.validate"}),
	}
	var mutex sync.Mutex
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			http.Error(writer, "unexpected path", http.StatusNotFound)
			return
		}
		mutex.Lock()
		index := requestCount
		requestCount++
		mutex.Unlock()
		if index >= len(actions) {
			http.Error(writer, "too many requests", http.StatusInternalServerError)
			return
		}
		beam, err := json.Marshal(action.Beam{Actions: []action.Candidate{actions[index]}})
		if err != nil {
			t.Errorf("marshal beam: %v", err)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(writer).Encode(map[string]any{
			"id":    "test-request",
			"model": "test-model",
			"choices": []map[string]any{{
				"message":       map[string]any{"content": string(beam)},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	root := commandRepoRoot(t)
	temporary := t.TempDir()
	resultPath := filepath.Join(temporary, "result.json")
	eventPath := filepath.Join(temporary, "events.jsonl")
	err := run(runOptions{
		ManifestPath:         filepath.Join(root, "configs/run-manifest.example.json"),
		TaskPath:             filepath.Join(root, "benchmarks/tasks/task-001.json"),
		Endpoint:             server.URL + "/v1",
		ProjectRoot:          root,
		OutputPath:           resultPath,
		EventLogPath:         eventPath,
		SeedOverride:         -1,
		PolicyEngine:         "go",
		ExecutorMode:         "virtual",
		TraceSampleRatio:     0,
		ObjectiveCalibration: filepath.Join(root, "configs/objective-calibration.bootstrap.json"),
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	mutex.Lock()
	gotRequestCount := requestCount
	mutex.Unlock()
	if gotRequestCount != len(actions) {
		t.Fatalf("provider received %d requests, want %d", gotRequestCount, len(actions))
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	var result control.Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Passed || !result.TaskComplete || result.ExecutedActions != 4 {
		t.Fatalf("unexpected result: %+v", result)
	}
	input, err := os.Open(eventPath)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	verification, err := eventlog.Verify(input)
	if err != nil {
		t.Fatalf("verify event log: %v", err)
	}
	if verification.TerminalEvent != "run.completed" || verification.TaskID != "task-001" {
		t.Fatalf("unexpected verification: %+v", verification)
	}
}

func TestRunRejectsConfigurationErrors(t *testing.T) {
	t.Parallel()
	root := commandRepoRoot(t)
	base := runOptions{
		ManifestPath:         filepath.Join(root, "configs/run-manifest.example.json"),
		TaskPath:             filepath.Join(root, "benchmarks/tasks/task-001.json"),
		ProjectRoot:          root,
		SeedOverride:         -1,
		PolicyEngine:         "go",
		ExecutorMode:         "virtual",
		TraceSampleRatio:     0,
		ObjectiveCalibration: filepath.Join(root, "configs/objective-calibration.bootstrap.json"),
	}
	tests := map[string]func(*runOptions){
		"policy engine": func(options *runOptions) { options.PolicyEngine = "unknown" },
		"executor mode": func(options *runOptions) { options.ExecutorMode = "unknown" },
		"calibration": func(options *runOptions) {
			options.ObjectiveCalibration = filepath.Join(t.TempDir(), "missing.json")
		},
		"routing": func(options *runOptions) {
			options.RoutingStrategy = "unknown"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			options := base
			mutate(&options)
			if err := run(options); err == nil {
				t.Fatal("run returned nil error")
			}
		})
	}
}

func TestCommandHelpersFailClosed(t *testing.T) {
	t.Parallel()
	if got := resolvePolicyEngine("go", "subprocess"); got != "python-subprocess" {
		t.Fatalf("resolvePolicyEngine returned %q", got)
	}
	if got := resolvePolicyEngine("python-subprocess", "persistent"); got != "conflicting-policy-engine-flags" {
		t.Fatalf("resolvePolicyEngine conflict returned %q", got)
	}
	if got := resolvePolicyEngine("go", "unknown"); got != "invalid-legacy-projector-mode" {
		t.Fatalf("resolvePolicyEngine invalid mode returned %q", got)
	}
	path := filepath.Join(t.TempDir(), "nested", "result.json")
	if err := writeExclusive(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusive(path, []byte("second")); err == nil {
		t.Fatal("writeExclusive overwrote an existing artifact")
	}
	if _, err := hashFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("hashFile returned nil error for a missing file")
	}
}

func commandCandidate(
	id string,
	operation string,
	tool string,
	arguments map[string]any,
	dependencies []string,
) action.Candidate {
	if dependencies == nil {
		dependencies = []string{}
	}
	return action.Candidate{
		CandidateID:          id,
		OperationClass:       operation,
		Tool:                 tool,
		Target:               "workspace/service/config.yaml",
		Arguments:            arguments,
		DeclaredDependencies: dependencies,
		EstimatedObjectives: action.Objectives{
			LatencyMS:  10,
			CostUnits:  0.01,
			SafetyRisk: 0.01,
		},
	}
}

func commandRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}
