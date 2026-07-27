package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bouncer/internal/action"
	"bouncer/internal/nimclient"
)

func TestRecordedProviderRequiresExactRequest(t *testing.T) {
	request := nimclient.ProposalRequest{
		TaskID:      "task-001",
		Instruction: "read the file",
		State:       json.RawMessage(`{"files":{}}`),
		Policy:      json.RawMessage(`{"allowed_operation_classes":["filesystem.read"]}`),
		ProposerID:  "agent-1",
		Seed:        42,
	}
	actions := make([]action.Candidate, 0, 2)
	for _, id := range []string{"candidate-1", "candidate-2"} {
		actions = append(actions, action.Candidate{
			CandidateID:          id,
			OperationClass:       "filesystem.read",
			Tool:                 "read_file",
			Target:               "workspace/file.txt",
			Arguments:            map[string]any{},
			DeclaredDependencies: []string{},
			EstimatedObjectives:  action.Objectives{},
		})
	}
	document := ReplayDocument{
		SchemaVersion: "0.1.0",
		Records: []ReplayRecord{{
			Request: request,
			Result: nimclient.ProposalResult{
				ProposerID:   "agent-1",
				Beam:         action.Beam{Actions: actions},
				FinishReason: "stop",
			},
		}},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "replay.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	replay, err := LoadReplay(path, 2)
	if err != nil {
		t.Fatalf("LoadReplay returned error: %v", err)
	}
	result, err := replay.Propose(context.Background(), request)
	if err != nil || len(result.Beam.Actions) != 2 {
		t.Fatalf("Propose result=%+v error=%v", result, err)
	}
	mismatch := request
	mismatch.Instruction = "delete the file"
	if _, err := replay.Propose(context.Background(), mismatch); err == nil ||
		!strings.Contains(err.Error(), "content mismatch") {
		t.Fatalf("mismatched request returned %v", err)
	}
}

func TestProviderFactoryRejectsUnknownKind(t *testing.T) {
	if _, err := New(Config{Kind: "unknown"}); err == nil {
		t.Fatal("New accepted an unknown provider kind")
	}
}
