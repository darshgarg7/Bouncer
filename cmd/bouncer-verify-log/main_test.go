package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bouncer/internal/eventlog"
)

func TestRunVerifiesExternalAnchor(t *testing.T) {
	t.Parallel()
	path, finalHash := commandEventLog(t, true)
	var output bytes.Buffer
	if err := run(path, finalHash, &output); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(output.String(), finalHash) || !strings.Contains(output.String(), "run.completed") {
		t.Fatalf("unexpected output: %s", output.String())
	}
	if err := run(path, strings.Repeat("0", 64), &output); err == nil {
		t.Fatal("run accepted the wrong external anchor")
	}
}

func TestRunRejectsMissingAndIncompleteLogs(t *testing.T) {
	t.Parallel()
	if err := run("", "", &bytes.Buffer{}); err == nil {
		t.Fatal("run accepted an empty path")
	}
	if err := run(filepath.Join(t.TempDir(), "missing.jsonl"), "", &bytes.Buffer{}); err == nil {
		t.Fatal("run accepted a missing path")
	}
	path, _ := commandEventLog(t, false)
	if err := run(path, "", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "missing terminal") {
		t.Fatalf("run returned %v for incomplete evidence", err)
	}
}

func commandEventLog(t *testing.T, terminal bool) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := eventlog.NewWriter(file)
	if err := writer.Append(eventlog.Event{
		EventType: "run.started",
		RunID:     "run",
		TaskID:    "task",
		Payload:   map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	if terminal {
		if err := writer.Append(eventlog.Event{
			EventType: "run.completed",
			RunID:     "run",
			TaskID:    "task",
			Payload:   map[string]any{},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	verification, err := eventlog.Verify(input)
	if !terminal {
		if err == nil {
			t.Fatal("expected incomplete verification error")
		}
		return path, ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return path, verification.FinalHash
}
