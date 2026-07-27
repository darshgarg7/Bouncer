package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"bouncer/internal/action"
	"bouncer/internal/anomaly"
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
		AnomalyMode:          control.AnomalyShadow,
		AnomalyArtifact:      filepath.Join(root, "configs/anomaly-artifact.bootstrap.json"),
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
	if !result.Passed || !result.TaskComplete || result.ExecutedActions != 4 ||
		result.AnomalyMode != control.AnomalyShadow || result.AnomalyArtifact == nil ||
		result.ExecutionGated {
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

func TestRunActiveAnomalyArtifactStopsAfterTriggeringExecution(t *testing.T) {
	t.Parallel()
	root := commandRepoRoot(t)
	fixtureData, err := os.ReadFile(filepath.Join(root, "examples/anomaly-score-fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Artifact anomaly.Artifact `json:"artifact"`
	}
	if err := json.Unmarshal(fixtureData, &fixture); err != nil {
		t.Fatal(err)
	}
	fixture.Artifact.ArtifactID = "command-active-gate-test"
	fixture.Artifact.ActiveEligible = true
	fixture.Artifact.Provenance.Validation = &anomaly.ValidationProvenance{
		DatasetSHA256:     "3333333333333333333333333333333333333333333333333333333333333333",
		Rows:              20,
		NormalRows:        10,
		AnomalyRows:       10,
		TruePositiveRate:  1,
		FalsePositiveRate: 0,
	}
	artifactData, err := json.MarshalIndent(fixture.Artifact, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	artifactPath := filepath.Join(temporary, "active-anomaly.json")
	if err := os.WriteFile(artifactPath, append(artifactData, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	denied := commandCandidate("denied", "filesystem.write", "apply_patch", map[string]any{
		"content": "must not execute\n",
	}, nil)
	denied.Target = "outside/config.yaml"
	beams := [][]action.Candidate{
		{
			denied,
			commandCandidate("read", "filesystem.read", "read_file", map[string]any{}, nil),
		},
		{commandCandidate("write", "filesystem.write", "apply_patch", map[string]any{
			"content": "timeout: 60\nretries: 3\n",
		}, []string{"filesystem.read"})},
	}
	var mutex sync.Mutex
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		index := requestCount
		requestCount++
		mutex.Unlock()
		if index >= len(beams) {
			http.Error(writer, "too many requests", http.StatusInternalServerError)
			return
		}
		beam, marshalErr := json.Marshal(action.Beam{Actions: beams[index]})
		if marshalErr != nil {
			t.Errorf("marshal beam: %v", marshalErr)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if encodeErr := json.NewEncoder(writer).Encode(map[string]any{
			"id":    "active-gate-request",
			"model": "test-model",
			"choices": []map[string]any{{
				"message":       map[string]any{"content": string(beam)},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15,
			},
		}); encodeErr != nil {
			t.Errorf("encode response: %v", encodeErr)
		}
	}))
	defer server.Close()

	resultPath := filepath.Join(temporary, "result.json")
	eventPath := filepath.Join(temporary, "events.jsonl")
	if err := run(runOptions{
		ManifestPath:         filepath.Join(root, "configs/run-manifest.example.json"),
		TaskPath:             filepath.Join(root, "benchmarks/tasks/task-001.json"),
		Endpoint:             server.URL + "/v1",
		ProjectRoot:          root,
		OutputPath:           resultPath,
		EventLogPath:         eventPath,
		SeedOverride:         -1,
		BeamOverride:         2,
		PolicyEngine:         "go",
		ExecutorMode:         "virtual",
		TraceSampleRatio:     0,
		ObjectiveCalibration: filepath.Join(root, "configs/objective-calibration.bootstrap.json"),
		AnomalyMode:          control.AnomalyActive,
		AnomalyArtifact:      artifactPath,
	}); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	mutex.Lock()
	gotRequestCount := requestCount
	mutex.Unlock()
	if gotRequestCount != 1 {
		t.Fatalf("active gate allowed %d provider requests, want 1", gotRequestCount)
	}
	resultData, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	var result control.Result
	if err := json.Unmarshal(resultData, &result); err != nil {
		t.Fatal(err)
	}
	if result.Passed || result.TaskComplete || !result.ExecutionGated ||
		result.ExecutedActions != 1 || result.ModelCalls != 1 || result.Turns != 1 ||
		result.ConstraintRejections != 1 || result.AnomalyAlerts != 1 ||
		result.AnomalyGates != 1 || result.AnomalyScoringErrors != 0 {
		t.Fatalf("unexpected active gate result: %+v", result)
	}

	eventData, err := os.ReadFile(eventPath)
	if err != nil {
		t.Fatal(err)
	}
	gateEvidence := false
	terminalCounters := false
	for _, line := range bytes.Split(bytes.TrimSpace(eventData), []byte{'\n'}) {
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		payload, _ := event["payload"].(map[string]any)
		switch event["event_type"] {
		case "execution.completed":
			monitoringEvidence, _ := payload["monitoring"].(map[string]any)
			anomalyEvidence, _ := monitoringEvidence["anomaly"].(map[string]any)
			gateEvidence = monitoringEvidence["subsequent_execution_gated"] == true &&
				anomalyEvidence["alert"] == true
		case "run.completed":
			terminalCounters = payload["anomaly_alerts"] == float64(1) &&
				payload["anomaly_gates"] == float64(1) && payload["execution_gated"] == true
		}
	}
	if !gateEvidence || !terminalCounters {
		t.Fatalf("missing active gate evidence: gate=%t terminal=%t", gateEvidence, terminalCounters)
	}
	verification, err := eventlog.Verify(bytes.NewReader(eventData))
	if err != nil {
		t.Fatal(err)
	}
	if verification.TerminalEvent != "run.completed" {
		t.Fatalf("unexpected terminal event: %+v", verification)
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
		"anomaly mode": func(options *runOptions) {
			options.AnomalyMode = "unknown"
		},
		"missing anomaly artifact": func(options *runOptions) {
			options.AnomalyMode = control.AnomalyShadow
			options.AnomalyArtifact = filepath.Join(t.TempDir(), "missing.json")
		},
		"shadow-only artifact in active mode": func(options *runOptions) {
			options.AnomalyMode = control.AnomalyActive
			options.AnomalyArtifact = filepath.Join(root, "configs/anomaly-artifact.bootstrap.json")
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
