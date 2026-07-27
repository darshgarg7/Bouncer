package eventlog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"bouncer/internal/control"
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
	if verification.Events != 3 || verification.FinalHash == GenesisHash ||
		verification.TerminalEvent != "run.completed" || verification.RunID != "run" {
		t.Fatalf("unexpected verification: %+v", verification)
	}
	tampered := strings.Replace(output.String(), "proposal.completed", "proposal.requested", 1)
	if _, err := Verify(strings.NewReader(tampered)); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("tampered verification returned %v", err)
	}
}

func TestVerifyRejectsTruncatedChainWithoutTerminalEvent(t *testing.T) {
	var output bytes.Buffer
	writer := NewWriter(&output)
	for _, eventType := range []string{"run.started", "proposal.completed", "run.completed"} {
		if err := writer.Append(Event{EventType: eventType, RunID: "run", TaskID: "task", Payload: map[string]any{}}); err != nil {
			t.Fatal(err)
		}
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	truncated := strings.Join(lines[:len(lines)-1], "\n") + "\n"
	if _, err := Verify(strings.NewReader(truncated)); err == nil || !strings.Contains(err.Error(), "missing terminal") {
		t.Fatalf("truncated verification returned %v", err)
	}
}

func TestVerifyRejectsInvalidLifecycleAndMixedIdentity(t *testing.T) {
	tests := []struct {
		name   string
		events []Event
		want   string
	}{
		{
			name: "missing start",
			events: []Event{
				{EventType: "proposal.completed", RunID: "run", TaskID: "task", Payload: map[string]any{}},
				{EventType: "run.completed", RunID: "run", TaskID: "task", Payload: map[string]any{}},
			},
			want: "expected run.started",
		},
		{
			name: "mixed identity",
			events: []Event{
				{EventType: "run.started", RunID: "run", TaskID: "task", Payload: map[string]any{}},
				{EventType: "run.completed", RunID: "other", TaskID: "task", Payload: map[string]any{}},
			},
			want: "inconsistent run or task identity",
		},
		{
			name: "event after terminal",
			events: []Event{
				{EventType: "run.started", RunID: "run", TaskID: "task", Payload: map[string]any{}},
				{EventType: "run.completed", RunID: "run", TaskID: "task", Payload: map[string]any{}},
				{EventType: "proposal.completed", RunID: "run", TaskID: "task", Payload: map[string]any{}},
			},
			want: "event follows terminal",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			writer := NewWriter(&output)
			for _, event := range test.events {
				if err := writer.Append(event); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Verify(bytes.NewReader(output.Bytes())); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Verify returned %v, want %q", err, test.want)
			}
		})
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
	if _, err := Verify(strings.NewReader(reordered)); err == nil || !strings.Contains(err.Error(), "expected run.started") {
		t.Fatalf("reordered verification returned %v", err)
	}
}

func TestVerifyRejectsMalformedEvidenceLines(t *testing.T) {
	var output bytes.Buffer
	writer := NewWriter(&output)
	for _, eventType := range []string{"run.started", "run.completed"} {
		if err := writer.Append(Event{
			EventType: eventType,
			RunID:     "run",
			TaskID:    "task",
			Payload:   map[string]any{},
		}); err != nil {
			t.Fatal(err)
		}
	}
	firstLine := strings.Split(strings.TrimSpace(output.String()), "\n")[0]
	tests := map[string]string{
		"invalid JSON":     "{\n",
		"unknown field":    strings.Replace(firstLine, "{", `{"unknown":true,`, 1) + "\n",
		"trailing content": firstLine + " {}\n",
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Verify(strings.NewReader(document)); err == nil {
				t.Fatal("Verify returned nil error")
			}
		})
	}
}

func TestVerifyRejectsDuplicateEventIDs(t *testing.T) {
	var output bytes.Buffer
	writer := NewWriter(&output)
	for _, eventType := range []string{"run.started", "run.completed"} {
		if err := writer.Append(Event{
			EventID:   "duplicate",
			EventType: eventType,
			RunID:     "run",
			TaskID:    "task",
			Payload:   map[string]any{},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Verify(bytes.NewReader(output.Bytes())); err == nil || !strings.Contains(err.Error(), "duplicate event_id") {
		t.Fatalf("Verify returned %v", err)
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

func TestEventValidationAndWriterFailurePaths(t *testing.T) {
	base := Event{
		SchemaVersion: SchemaVersion,
		EventID:       "event",
		EventType:     "run.started",
		RunID:         "run",
		TaskID:        "task",
		Attempt:       1,
		Sequence:      1,
		Timestamp:     time.Now().UTC(),
		Payload:       map[string]any{},
		PreviousHash:  GenesisHash,
		Hash:          GenesisHash,
	}
	tests := map[string]func(*Event){
		"schema":        func(event *Event) { event.SchemaVersion = "old" },
		"type":          func(event *Event) { event.EventType = "unknown" },
		"identity":      func(event *Event) { event.RunID = "" },
		"step":          func(event *Event) { event.StepID = -1 },
		"attempt":       func(event *Event) { event.Attempt = 0 },
		"sequence":      func(event *Event) { event.Sequence = 0 },
		"timestamp":     func(event *Event) { event.Timestamp = time.Time{} },
		"payload":       func(event *Event) { event.Payload = nil },
		"previous hash": func(event *Event) { event.PreviousHash = "bad" },
		"hash":          func(event *Event) { event.Hash = strings.Repeat("A", 64) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			event := base
			mutate(&event)
			if err := event.Validate(); err == nil {
				t.Fatal("Validate accepted malformed event")
			}
		})
	}

	var output bytes.Buffer
	writer := NewWriter(&output)
	valid := Event{EventType: "run.started", RunID: "run", TaskID: "task", Payload: map[string]any{}}
	invalidSequence := valid
	invalidSequence.Sequence = 2
	if err := writer.Append(invalidSequence); err == nil {
		t.Fatal("writer accepted non-monotonic sequence")
	}
	invalidPrevious := valid
	invalidPrevious.PreviousHash = strings.Repeat("a", 64)
	if err := writer.Append(invalidPrevious); err == nil {
		t.Fatal("writer accepted wrong previous hash")
	}
	invalidHash := valid
	invalidHash.Hash = strings.Repeat("a", 64)
	if err := writer.Append(invalidHash); err == nil {
		t.Fatal("writer accepted wrong content hash")
	}
}

type failingSyncWriter struct{ bytes.Buffer }

func (failingSyncWriter) Sync() error { return errors.New("sync failed") }

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestWriterAndTraceSinkPropagateStorageAndContextFailures(t *testing.T) {
	event := Event{EventType: "run.started", RunID: "run", TaskID: "task", Payload: map[string]any{}}
	if err := NewWriter(&failingSyncWriter{}).Append(event); err == nil || !strings.Contains(err.Error(), "sync") {
		t.Fatalf("sync failure returned %v", err)
	}
	if err := NewWriter(failingWriter{}).Append(event); err == nil || !strings.Contains(err.Error(), "append event") {
		t.Fatalf("write failure returned %v", err)
	}
	trace := control.TraceEvent{EventType: "run.started", Payload: map[string]any{}}
	if err := (TraceSink{}).Append(context.Background(), trace); err == nil {
		t.Fatal("nil trace writer was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (TraceSink{}).Append(ctx, trace); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled trace returned %v", err)
	}
	var output bytes.Buffer
	sink := TraceSink{Writer: NewWriter(&output), RunID: "run", TaskID: "task", Seed: 7}
	if err := sink.Append(context.Background(), trace); err != nil {
		t.Fatal(err)
	}
}

func FuzzVerifyNeverPanics(f *testing.F) {
	var valid bytes.Buffer
	writer := NewWriter(&valid)
	for _, eventType := range []string{"run.started", "proposal.completed", "run.completed"} {
		if err := writer.Append(Event{
			EventType: eventType,
			RunID:     "fuzz-run",
			TaskID:    "fuzz-task",
			Payload:   map[string]any{"event": eventType},
		}); err != nil {
			f.Fatal(err)
		}
	}
	f.Add(valid.Bytes())
	f.Add([]byte(`{"schema_version":"0.2.0"}` + "\n"))
	f.Add([]byte("not-json\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Verify(bytes.NewReader(data))
	})
}
