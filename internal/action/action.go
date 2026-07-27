package action

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
)

const BeamWidth = 5

var candidateIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

var allowedOperations = map[string]struct{}{
	"filesystem.read":   {},
	"filesystem.write":  {},
	"filesystem.delete": {},
	"state.validate":    {},
	"state.backup":      {},
	"command.run":       {},
	"service.deploy":    {},
	"task.complete":     {},
}

type Objectives struct {
	LatencyMS  float64 `json:"latency_ms"`
	CostUnits  float64 `json:"cost_units"`
	SafetyRisk float64 `json:"safety_risk"`
}

type Candidate struct {
	CandidateID          string         `json:"candidate_id"`
	OperationClass       string         `json:"operation_class"`
	Tool                 string         `json:"tool"`
	Target               string         `json:"target"`
	Arguments            map[string]any `json:"arguments"`
	DeclaredDependencies []string       `json:"declared_dependencies"`
	EstimatedObjectives  Objectives     `json:"estimated_objectives"`
}

// ScoredCandidate keeps untrusted provider estimates separate from the
// objectives that the trusted router is allowed to consume.
type ScoredCandidate struct {
	Candidate         Candidate  `json:"candidate"`
	RoutingObjectives Objectives `json:"routing_objectives"`
}

type Beam struct {
	Actions []Candidate `json:"actions"`
}

func DecodeBeamStrict(data []byte) (Beam, error) {
	return DecodeBeamStrictWidth(data, BeamWidth)
}

func DecodeBeamStrictWidth(data []byte, width int) (Beam, error) {
	var beam Beam
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&beam); err != nil {
		return Beam{}, fmt.Errorf("decode proposal beam: %w", err)
	}
	if err := requireEOF(decoder); err != nil {
		return Beam{}, err
	}
	if err := beam.ValidateWidth(width); err != nil {
		return Beam{}, err
	}
	return beam, nil
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("decode proposal beam: trailing JSON value")
	}
	return fmt.Errorf("decode proposal beam trailing content: %w", err)
}

func (b Beam) Validate() error {
	return b.ValidateWidth(BeamWidth)
}

func (b Beam) ValidateWidth(width int) error {
	if width <= 0 || width > 16 {
		return fmt.Errorf("proposal beam width must be between 1 and 16, got %d", width)
	}
	if len(b.Actions) != width {
		return fmt.Errorf("proposal beam must contain exactly %d actions, got %d", width, len(b.Actions))
	}
	seen := make(map[string]struct{}, len(b.Actions))
	for i := range b.Actions {
		if err := b.Actions[i].Validate(); err != nil {
			return fmt.Errorf("action %d: %w", i, err)
		}
		if _, ok := seen[b.Actions[i].CandidateID]; ok {
			return fmt.Errorf("action %d: duplicate candidate_id %q", i, b.Actions[i].CandidateID)
		}
		seen[b.Actions[i].CandidateID] = struct{}{}
	}
	return nil
}

func (c Candidate) Validate() error {
	if !candidateIDPattern.MatchString(c.CandidateID) {
		return fmt.Errorf("invalid candidate_id %q", c.CandidateID)
	}
	if _, ok := allowedOperations[c.OperationClass]; !ok {
		return fmt.Errorf("unknown operation_class %q", c.OperationClass)
	}
	if strings.TrimSpace(c.Tool) == "" || len(c.Tool) > 128 {
		return errors.New("tool must contain 1 to 128 non-whitespace characters")
	}
	if strings.TrimSpace(c.Target) == "" || len(c.Target) > 1024 {
		return errors.New("target must contain 1 to 1024 non-whitespace characters")
	}
	if c.Arguments == nil {
		return errors.New("arguments must be an object")
	}
	dependencies := make(map[string]struct{}, len(c.DeclaredDependencies))
	for _, dependency := range c.DeclaredDependencies {
		if strings.TrimSpace(dependency) == "" || len(dependency) > 128 {
			return errors.New("declared_dependencies entries must contain 1 to 128 non-whitespace characters")
		}
		if _, ok := dependencies[dependency]; ok {
			return fmt.Errorf("duplicate declared dependency %q", dependency)
		}
		dependencies[dependency] = struct{}{}
	}
	if err := c.EstimatedObjectives.Validate(); err != nil {
		return err
	}
	return nil
}

func (o Objectives) Validate() error {
	values := []struct {
		name  string
		value float64
	}{
		{name: "latency_ms", value: o.LatencyMS},
		{name: "cost_units", value: o.CostUnits},
		{name: "safety_risk", value: o.SafetyRisk},
	}
	for _, item := range values {
		if math.IsNaN(item.value) || math.IsInf(item.value, 0) {
			return fmt.Errorf("%s must be finite", item.name)
		}
		if item.value < 0 {
			return fmt.Errorf("%s must not be negative", item.name)
		}
	}
	if o.SafetyRisk > 1 {
		return errors.New("safety_risk must be between 0 and 1")
	}
	return nil
}

// Validate checks both the provider candidate and its trusted routing score.
func (s ScoredCandidate) Validate() error {
	if err := s.Candidate.Validate(); err != nil {
		return err
	}
	if err := s.RoutingObjectives.Validate(); err != nil {
		return fmt.Errorf("routing objectives: %w", err)
	}
	return nil
}
