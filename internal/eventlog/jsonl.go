package eventlog

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	SchemaVersion = "0.2.0"
	GenesisHash   = "0000000000000000000000000000000000000000000000000000000000000000"
)

var eventTypes = map[string]struct{}{
	"run.started":          {},
	"run.completed":        {},
	"run.failed":           {},
	"proposal.requested":   {},
	"proposal.completed":   {},
	"proposal.expanded":    {},
	"proposal.failed":      {},
	"constraint.evaluated": {},
	"candidate.selected":   {},
	"execution.completed":  {},
	"task.completed":       {},
}

type Event struct {
	SchemaVersion string         `json:"schema_version"`
	EventID       string         `json:"event_id"`
	EventType     string         `json:"event_type"`
	RunID         string         `json:"run_id"`
	TaskID        string         `json:"task_id"`
	StepID        int            `json:"step_id"`
	Attempt       int            `json:"attempt"`
	Sequence      uint64         `json:"sequence"`
	Seed          int64          `json:"seed,omitempty"`
	Timestamp     time.Time      `json:"timestamp"`
	Payload       map[string]any `json:"payload"`
	PreviousHash  string         `json:"previous_hash"`
	Hash          string         `json:"hash"`
}

type Writer struct {
	mutex        sync.Mutex
	encoder      *json.Encoder
	output       io.Writer
	previousHash string
	sequence     uint64
}

func NewWriter(output io.Writer) *Writer {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return &Writer{encoder: encoder, output: output, previousHash: GenesisHash}
}

func (w *Writer) Append(event Event) error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if event.SchemaVersion == "" {
		event.SchemaVersion = SchemaVersion
	}
	if event.EventID == "" {
		id, err := NewID()
		if err != nil {
			return err
		}
		event.EventID = id
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.Attempt == 0 {
		event.Attempt = 1
	}
	expectedSequence := w.sequence + 1
	if event.Sequence != 0 && event.Sequence != expectedSequence {
		return errors.New("event sequence does not match writer sequence")
	}
	event.Sequence = expectedSequence
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	if event.PreviousHash != "" && event.PreviousHash != w.previousHash {
		return errors.New("event previous_hash does not match writer chain head")
	}
	event.PreviousHash = w.previousHash
	hash, err := computeHash(event)
	if err != nil {
		return err
	}
	if event.Hash != "" && event.Hash != hash {
		return errors.New("event hash does not match canonical content")
	}
	event.Hash = hash
	if err := event.Validate(); err != nil {
		return err
	}
	if err := w.encoder.Encode(event); err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	if syncer, ok := w.output.(interface{ Sync() error }); ok {
		if err := syncer.Sync(); err != nil {
			return fmt.Errorf("sync event: %w", err)
		}
	}
	w.previousHash = event.Hash
	w.sequence = event.Sequence
	return nil
}

func (e Event) Validate() error {
	if e.SchemaVersion != SchemaVersion {
		return fmt.Errorf("event schema_version must be %s", SchemaVersion)
	}
	if _, ok := eventTypes[e.EventType]; !ok {
		return fmt.Errorf("unsupported event_type %q", e.EventType)
	}
	if e.EventID == "" || e.RunID == "" || e.TaskID == "" {
		return errors.New("event_id, run_id, and task_id are required")
	}
	if e.StepID < 0 || e.Attempt < 1 || e.Sequence < 1 {
		return errors.New("event step_id must be non-negative and attempt and sequence must be positive")
	}
	if e.Timestamp.IsZero() {
		return errors.New("event timestamp is required")
	}
	if e.Payload == nil {
		return errors.New("event payload is required")
	}
	if !validHash(e.PreviousHash) || !validHash(e.Hash) {
		return errors.New("event previous_hash and hash must be lowercase SHA-256 hex")
	}
	return nil
}

type Verification struct {
	Events        int    `json:"events"`
	RunID         string `json:"run_id"`
	TaskID        string `json:"task_id"`
	TerminalEvent string `json:"terminal_event"`
	FinalHash     string `json:"final_hash"`
}

// Verify checks lifecycle semantics, run identity, event IDs, canonical content
// hashes, and links. A completed log must start with run.started and end with one
// terminal run.completed or run.failed event. External storage of FinalHash is
// still required to detect an attacker who can rewrite the entire chain.
func Verify(input io.Reader) (Verification, error) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	previous := GenesisHash
	count := 0
	runID := ""
	taskID := ""
	terminalEvent := ""
	eventIDs := map[string]struct{}{}
	for scanner.Scan() {
		count++
		var event Event
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil {
			return Verification{}, fmt.Errorf("event line %d: decode: %w", count, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return Verification{}, fmt.Errorf("event line %d: decode: invalid trailing content", count)
		}
		if err := event.Validate(); err != nil {
			return Verification{}, fmt.Errorf("event line %d: %w", count, err)
		}
		if _, duplicate := eventIDs[event.EventID]; duplicate {
			return Verification{}, fmt.Errorf("event line %d: duplicate event_id", count)
		}
		eventIDs[event.EventID] = struct{}{}
		if count == 1 {
			if event.EventType != "run.started" {
				return Verification{}, errors.New("event line 1: expected run.started")
			}
			runID = event.RunID
			taskID = event.TaskID
		} else {
			if terminalEvent != "" {
				return Verification{}, fmt.Errorf("event line %d: event follows terminal %s", count, terminalEvent)
			}
			if event.EventType == "run.started" {
				return Verification{}, fmt.Errorf("event line %d: duplicate run.started", count)
			}
			if event.RunID != runID || event.TaskID != taskID {
				return Verification{}, fmt.Errorf("event line %d: inconsistent run or task identity", count)
			}
		}
		if event.PreviousHash != previous {
			return Verification{}, fmt.Errorf("event line %d: broken previous_hash link", count)
		}
		if event.Sequence != uint64(count) {
			return Verification{}, fmt.Errorf("event line %d: non-monotonic sequence", count)
		}
		expected, err := computeHash(event)
		if err != nil {
			return Verification{}, fmt.Errorf("event line %d: %w", count, err)
		}
		if event.Hash != expected {
			return Verification{}, fmt.Errorf("event line %d: content hash mismatch", count)
		}
		if event.EventType == "run.completed" || event.EventType == "run.failed" {
			terminalEvent = event.EventType
		}
		previous = event.Hash
	}
	if err := scanner.Err(); err != nil {
		return Verification{}, fmt.Errorf("scan event log: %w", err)
	}
	if count == 0 {
		return Verification{}, errors.New("event log is empty")
	}
	if terminalEvent == "" {
		return Verification{}, errors.New("event log is incomplete: missing terminal run.completed or run.failed")
	}
	return Verification{
		Events:        count,
		RunID:         runID,
		TaskID:        taskID,
		TerminalEvent: terminalEvent,
		FinalHash:     previous,
	}, nil
}

func computeHash(event Event) (string, error) {
	payload := struct {
		SchemaVersion string         `json:"schema_version"`
		EventID       string         `json:"event_id"`
		EventType     string         `json:"event_type"`
		RunID         string         `json:"run_id"`
		TaskID        string         `json:"task_id"`
		StepID        int            `json:"step_id"`
		Attempt       int            `json:"attempt"`
		Sequence      uint64         `json:"sequence"`
		Seed          int64          `json:"seed,omitempty"`
		Timestamp     time.Time      `json:"timestamp"`
		Payload       map[string]any `json:"payload"`
		PreviousHash  string         `json:"previous_hash"`
	}{
		SchemaVersion: event.SchemaVersion,
		EventID:       event.EventID,
		EventType:     event.EventType,
		RunID:         event.RunID,
		TaskID:        event.TaskID,
		StepID:        event.StepID,
		Attempt:       event.Attempt,
		Sequence:      event.Sequence,
		Seed:          event.Seed,
		Timestamp:     event.Timestamp,
		Payload:       event.Payload,
		PreviousHash:  event.PreviousHash,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("canonicalize event: %w", err)
	}
	var canonical any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&canonical); err != nil {
		return "", fmt.Errorf("normalize event: %w", err)
	}
	encoded, err = json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode normalized event: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validHash(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func NewID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate event id: %w", err)
	}
	return hex.EncodeToString(data), nil
}
