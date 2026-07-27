package action

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestDecodeBeamStrictAcceptsFiveValidActions(t *testing.T) {
	beam, err := DecodeBeamStrict([]byte(validBeamJSON()))
	if err != nil {
		t.Fatalf("DecodeBeamStrict returned error: %v", err)
	}
	if got := len(beam.Actions); got != BeamWidth {
		t.Fatalf("got %d actions, want %d", got, BeamWidth)
	}
}

func TestDecodeBeamStrictRejectsUnknownField(t *testing.T) {
	data := strings.Replace(validBeamJSON(), `"actions":`, `"unexpected":true,"actions":`, 1)
	if _, err := DecodeBeamStrict([]byte(data)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("got error %v, want unknown field", err)
	}
}

func TestDecodeBeamStrictRejectsTrailingJSON(t *testing.T) {
	if _, err := DecodeBeamStrict([]byte(validBeamJSON() + `{}`)); err == nil || !strings.Contains(err.Error(), "trailing JSON value") {
		t.Fatalf("got error %v, want trailing JSON value", err)
	}
}

func TestDecodeBeamStrictRejectsDuplicateCandidateID(t *testing.T) {
	data := strings.Replace(validBeamJSON(), `"candidate_id":"candidate-2"`, `"candidate_id":"candidate-1"`, 1)
	if _, err := DecodeBeamStrict([]byte(data)); err == nil || !strings.Contains(err.Error(), "duplicate candidate_id") {
		t.Fatalf("got error %v, want duplicate candidate_id", err)
	}
}

func TestObjectivesRejectOutOfRangeRisk(t *testing.T) {
	objectives := Objectives{LatencyMS: 1, CostUnits: 1, SafetyRisk: 1.1}
	if err := objectives.Validate(); err == nil {
		t.Fatal("Validate returned nil for out-of-range safety risk")
	}
}

func TestDecodeBeamStrictWidthSupportsAblationWidth(t *testing.T) {
	data := []byte(validBeamJSON())
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	actions := document["actions"].([]any)
	document["actions"] = actions[:3]
	ablationData, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	beam, err := DecodeBeamStrictWidth(ablationData, 3)
	if err != nil {
		t.Fatalf("DecodeBeamStrictWidth returned error: %v", err)
	}
	if len(beam.Actions) != 3 {
		t.Fatalf("got %d actions", len(beam.Actions))
	}
	if _, err := DecodeBeamStrictWidth(ablationData, 5); err == nil {
		t.Fatal("five-action decoder accepted a three-action beam")
	}
}

func validBeamJSON() string {
	var builder strings.Builder
	builder.WriteString(`{"actions":[`)
	for index := 1; index <= BeamWidth; index++ {
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

func FuzzDecodeBeamNeverPanics(f *testing.F) {
	f.Add([]byte(`{"actions":[]}`), 1)
	f.Add([]byte(`not-json`), 5)
	f.Fuzz(func(t *testing.T, data []byte, width int) {
		if width < 1 || width > 16 {
			width = 1
		}
		_, _ = DecodeBeamStrictWidth(data, width)
	})
}
