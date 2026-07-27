package eventlog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWriterAppendsIndependentJSONLines(t *testing.T) {
	var output bytes.Buffer
	writer := NewWriter(&output)
	for _, eventType := range []string{"proposal.requested", "proposal.completed"} {
		if err := writer.Append(Event{
			EventType: eventType,
			RunID:     "run",
			TaskID:    "task-001",
			Payload:   map[string]any{"ok": true},
		}); err != nil {
			t.Fatalf("Append returned error: %v", err)
		}
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	ids := map[string]bool{}
	for _, line := range lines {
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("line is not JSON: %v", err)
		}
		if event.SchemaVersion != SchemaVersion || event.EventID == "" || event.Hash == "" {
			t.Fatalf("event defaults were not populated: %+v", event)
		}
		ids[event.EventID] = true
		if event.Sequence == 0 {
			t.Fatal("event sequence was not populated")
		}
	}
	if len(ids) != 2 {
		t.Fatalf("event IDs are not unique: %v", ids)
	}
}

func TestVerifyAcceptsIntactChainAndRejectsTampering(t *testing.T) {
	var output bytes.Buffer
	writer := NewWriter(&output)
	for _, eventType := range []string{"run.started", "proposal.completed", "run.completed"} {
		if err := writer.Append(Event{
			EventType: eventType,
			RunID:     "run",
			TaskID:    "task-001",
			Payload:   map[string]any{"value": eventType},
		}); err != nil {
			t.Fatal(err)
		}
	}
	verification, err := Verify(bytes.NewReader(output.Bytes()))
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if verification.Events != 3 || verification.FinalHash == GenesisHash {
		t.Fatalf("unexpected verification: %+v", verification)
	}
	tampered := strings.Replace(output.String(), "proposal.completed", "proposal.requested", 1)
	if _, err := Verify(strings.NewReader(tampered)); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("tampered verification returned %v", err)
	}
}

func TestVerifyRejectsReorderedChain(t *testing.T) {
	var output bytes.Buffer
	writer := NewWriter(&output)
	for _, eventType := range []string{"run.started", "run.completed"} {
		if err := writer.Append(Event{EventType: eventType, RunID: "run", TaskID: "task", Payload: map[string]any{}}); err != nil {
			t.Fatal(err)
		}
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	reordered := lines[1] + "\n" + lines[0] + "\n"
	if _, err := Verify(strings.NewReader(reordered)); err == nil || !strings.Contains(err.Error(), "broken previous_hash") {
		t.Fatalf("reordered verification returned %v", err)
	}
}

func TestWriterRejectsUnknownEventType(t *testing.T) {
	var output bytes.Buffer
	err := NewWriter(&output).Append(Event{
		EventType: "model.made_something_up",
		RunID:     "run",
		TaskID:    "task-001",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported event_type") {
		t.Fatalf("got error %v", err)
	}
	if output.Len() != 0 {
		t.Fatal("invalid event was written")
	}
}
