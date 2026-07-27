package calibration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"bouncer/internal/action"
)

const schemaVersion = "0.1.0"

// Artifact is the complete, versioned policy for converting provider
// estimates into trusted router inputs.
type Artifact struct {
	SchemaVersion   string                       `json:"schema_version"`
	CalibrationID   string                       `json:"calibration_id"`
	Provenance      Provenance                   `json:"provenance"`
	InputBounds     ObjectiveBounds              `json:"input_bounds"`
	Transforms      Transforms                   `json:"transforms"`
	ModelInfluence  ModelInfluence               `json:"model_influence"`
	OperationPriors map[string]action.Objectives `json:"operation_priors"`
}

// Provenance records how much evidence was used to produce an artifact.
type Provenance struct {
	Method          string `json:"method"`
	DatasetSHA256   string `json:"dataset_sha256,omitempty"`
	SampleCount     int    `json:"sample_count"`
	ValidationCount int    `json:"validation_count"`
}

// Range bounds one provider-authored numeric field before transformation.
type Range struct {
	Minimum float64 `json:"minimum"`
	Maximum float64 `json:"maximum"`
}

// ObjectiveBounds defines independent accepted ranges for all three inputs.
type ObjectiveBounds struct {
	LatencyMS  Range `json:"latency_ms"`
	CostUnits  Range `json:"cost_units"`
	SafetyRisk Range `json:"safety_risk"`
}

// AffineTransform calibrates a non-negative continuous estimate.
type AffineTransform struct {
	Scale  float64 `json:"scale"`
	Offset float64 `json:"offset"`
}

// PlattTransform calibrates a probability in log-odds space.
type PlattTransform struct {
	Slope     float64 `json:"slope"`
	Intercept float64 `json:"intercept"`
}

// Transforms holds the fitted scaler for each objective.
type Transforms struct {
	LatencyMS  AffineTransform `json:"latency_ms"`
	CostUnits  AffineTransform `json:"cost_units"`
	SafetyRisk PlattTransform  `json:"safety_risk"`
}

// ModelInfluence controls how much each calibrated model estimate can change
// its operation-level prior. Zero removes model control of that objective.
type ModelInfluence struct {
	LatencyMS  float64 `json:"latency_ms"`
	CostUnits  float64 `json:"cost_units"`
	SafetyRisk float64 `json:"safety_risk"`
}

// Metadata is stable evidence attached to every routing decision.
type Metadata struct {
	SchemaVersion  string         `json:"schema_version"`
	CalibrationID  string         `json:"calibration_id"`
	ArtifactSHA256 string         `json:"artifact_sha256"`
	Provenance     Provenance     `json:"provenance"`
	ModelInfluence ModelInfluence `json:"model_influence"`
}

// Record explains the complete transformation for one candidate.
type Record struct {
	CandidateID           string            `json:"candidate_id"`
	OperationClass        string            `json:"operation_class"`
	RawObjectives         action.Objectives `json:"raw_objectives"`
	BoundedObjectives     action.Objectives `json:"bounded_objectives"`
	TransformedObjectives action.Objectives `json:"transformed_objectives"`
	OperationPrior        action.Objectives `json:"operation_prior"`
	RoutingObjectives     action.Objectives `json:"routing_objectives"`
}

// Batch contains router-ready candidates and their audit records.
type Batch struct {
	Candidates []action.ScoredCandidate `json:"candidates"`
	Records    []Record                 `json:"records"`
}

// Calibrator is the control-loop boundary between provider estimates and the
// router. Implementations must be deterministic for the same artifact/input.
type Calibrator interface {
	Calibrate([]action.Candidate) (Batch, error)
	Metadata() Metadata
}

// Runtime is an immutable, validated calibration artifact ready for use.
type Runtime struct {
	artifact Artifact
	metadata Metadata
}

// Load reads and strictly validates a calibration artifact.
func Load(path string) (*Runtime, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read objective calibration artifact: %w", err)
	}
	artifact, err := decodeStrict(data)
	if err != nil {
		return nil, err
	}
	runtime, err := New(artifact)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	runtime.metadata.ArtifactSHA256 = hex.EncodeToString(digest[:])
	return runtime, nil
}

// New validates an in-memory artifact and returns an immutable runtime.
func New(artifact Artifact) (*Runtime, error) {
	if err := artifact.Validate(); err != nil {
		return nil, fmt.Errorf("validate objective calibration artifact: %w", err)
	}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		return nil, fmt.Errorf("encode objective calibration artifact: %w", err)
	}
	// Decode the canonical bytes into a private copy so callers cannot mutate
	// an operation-prior map after its digest has been recorded.
	var frozen Artifact
	if err := json.Unmarshal(encoded, &frozen); err != nil {
		return nil, fmt.Errorf("copy objective calibration artifact: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return &Runtime{
		artifact: frozen,
		metadata: Metadata{
			SchemaVersion:  artifact.SchemaVersion,
			CalibrationID:  artifact.CalibrationID,
			ArtifactSHA256: hex.EncodeToString(digest[:]),
			Provenance:     artifact.Provenance,
			ModelInfluence: artifact.ModelInfluence,
		},
	}, nil
}

// Metadata returns the identity and evidence boundary of this runtime.
func (r *Runtime) Metadata() Metadata {
	return r.metadata
}

// Calibrate produces distinct routing scores without changing raw candidates.
func (r *Runtime) Calibrate(candidates []action.Candidate) (Batch, error) {
	if r == nil {
		return Batch{}, errors.New("objective calibrator is nil")
	}
	batch := Batch{
		Candidates: make([]action.ScoredCandidate, 0, len(candidates)),
		Records:    make([]Record, 0, len(candidates)),
	}
	for index, candidate := range candidates {
		if err := candidate.EstimatedObjectives.Validate(); err != nil {
			return Batch{}, fmt.Errorf("candidate %d raw objectives: %w", index, err)
		}
		prior, ok := r.artifact.OperationPriors[candidate.OperationClass]
		if !ok {
			prior = r.artifact.OperationPriors["*"]
		}
		bounded := bound(candidate.EstimatedObjectives, r.artifact.InputBounds)
		transformed := transform(bounded, r.artifact.Transforms)
		routing := blend(prior, transformed, r.artifact.ModelInfluence)
		batch.Candidates = append(batch.Candidates, action.ScoredCandidate{
			Candidate:         candidate,
			RoutingObjectives: routing,
		})
		batch.Records = append(batch.Records, Record{
			CandidateID:           candidate.CandidateID,
			OperationClass:        candidate.OperationClass,
			RawObjectives:         candidate.EstimatedObjectives,
			BoundedObjectives:     bounded,
			TransformedObjectives: transformed,
			OperationPrior:        prior,
			RoutingObjectives:     routing,
		})
	}
	return batch, nil
}

// Validate rejects ambiguous or unsafe calibration parameters.
func (a Artifact) Validate() error {
	if a.SchemaVersion != schemaVersion {
		return fmt.Errorf("schema_version must be %s", schemaVersion)
	}
	if strings.TrimSpace(a.CalibrationID) == "" || len(a.CalibrationID) > 128 {
		return errors.New("calibration_id must contain 1 to 128 non-whitespace characters")
	}
	if strings.TrimSpace(a.Provenance.Method) == "" {
		return errors.New("provenance method is required")
	}
	if a.Provenance.SampleCount < 0 || a.Provenance.ValidationCount < 0 ||
		a.Provenance.ValidationCount > a.Provenance.SampleCount {
		return errors.New("provenance sample counts are invalid")
	}
	if digest := a.Provenance.DatasetSHA256; digest != "" && !isSHA256(digest) {
		return errors.New("provenance dataset_sha256 must contain 64 lowercase hexadecimal characters")
	}
	if err := validateBounds(a.InputBounds); err != nil {
		return err
	}
	if err := validateTransforms(a.Transforms); err != nil {
		return err
	}
	if err := validateInfluence(a.ModelInfluence); err != nil {
		return err
	}
	if len(a.OperationPriors) == 0 {
		return errors.New("operation_priors must not be empty")
	}
	if _, ok := a.OperationPriors["*"]; !ok {
		return errors.New("operation_priors must include a * fallback")
	}
	for operation, prior := range a.OperationPriors {
		if strings.TrimSpace(operation) == "" {
			return errors.New("operation_priors contains an empty operation class")
		}
		if err := prior.Validate(); err != nil {
			return fmt.Errorf("operation prior %q: %w", operation, err)
		}
	}
	return nil
}

func decodeStrict(data []byte) (Artifact, error) {
	var artifact Artifact
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&artifact); err != nil {
		return Artifact{}, fmt.Errorf("decode objective calibration artifact: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Artifact{}, errors.New("decode objective calibration artifact: trailing JSON value")
		}
		return Artifact{}, fmt.Errorf("decode objective calibration artifact trailing content: %w", err)
	}
	return artifact, nil
}

func validateBounds(bounds ObjectiveBounds) error {
	ranges := []struct {
		name  string
		value Range
	}{
		{name: "latency_ms", value: bounds.LatencyMS},
		{name: "cost_units", value: bounds.CostUnits},
		{name: "safety_risk", value: bounds.SafetyRisk},
	}
	for _, item := range ranges {
		if !finite(item.value.Minimum) || !finite(item.value.Maximum) ||
			item.value.Minimum < 0 || item.value.Maximum <= item.value.Minimum {
			return fmt.Errorf("input bound %s must be finite, non-negative, and increasing", item.name)
		}
	}
	if bounds.SafetyRisk.Maximum > 1 {
		return errors.New("input bound safety_risk maximum must not exceed 1")
	}
	return nil
}

func validateTransforms(transforms Transforms) error {
	affine := []struct {
		name  string
		value AffineTransform
	}{
		{name: "latency_ms", value: transforms.LatencyMS},
		{name: "cost_units", value: transforms.CostUnits},
	}
	for _, item := range affine {
		if !finite(item.value.Scale) || !finite(item.value.Offset) || item.value.Scale < 0 {
			return fmt.Errorf("transform %s must have a finite non-negative scale and finite offset", item.name)
		}
	}
	if !finite(transforms.SafetyRisk.Slope) ||
		!finite(transforms.SafetyRisk.Intercept) ||
		transforms.SafetyRisk.Slope < 0 {
		return errors.New("safety_risk Platt transform must have a finite non-negative slope and finite intercept")
	}
	return nil
}

func validateInfluence(influence ModelInfluence) error {
	values := []struct {
		name  string
		value float64
	}{
		{name: "latency_ms", value: influence.LatencyMS},
		{name: "cost_units", value: influence.CostUnits},
		{name: "safety_risk", value: influence.SafetyRisk},
	}
	for _, item := range values {
		if !finite(item.value) || item.value < 0 || item.value > 1 {
			return fmt.Errorf("model influence %s must be between 0 and 1", item.name)
		}
	}
	return nil
}

func bound(raw action.Objectives, bounds ObjectiveBounds) action.Objectives {
	return action.Objectives{
		LatencyMS:  clamp(raw.LatencyMS, bounds.LatencyMS.Minimum, bounds.LatencyMS.Maximum),
		CostUnits:  clamp(raw.CostUnits, bounds.CostUnits.Minimum, bounds.CostUnits.Maximum),
		SafetyRisk: clamp(raw.SafetyRisk, bounds.SafetyRisk.Minimum, bounds.SafetyRisk.Maximum),
	}
}

func transform(bounded action.Objectives, transforms Transforms) action.Objectives {
	return action.Objectives{
		LatencyMS: math.Max(
			0,
			transforms.LatencyMS.Scale*bounded.LatencyMS+transforms.LatencyMS.Offset,
		),
		CostUnits: math.Max(
			0,
			transforms.CostUnits.Scale*bounded.CostUnits+transforms.CostUnits.Offset,
		),
		SafetyRisk: platt(bounded.SafetyRisk, transforms.SafetyRisk),
	}
}

func blend(
	prior action.Objectives,
	transformed action.Objectives,
	influence ModelInfluence,
) action.Objectives {
	return action.Objectives{
		LatencyMS: mix(prior.LatencyMS, transformed.LatencyMS, influence.LatencyMS),
		CostUnits: mix(prior.CostUnits, transformed.CostUnits, influence.CostUnits),
		SafetyRisk: clamp(
			mix(prior.SafetyRisk, transformed.SafetyRisk, influence.SafetyRisk),
			0,
			1,
		),
	}
}

func platt(probability float64, transform PlattTransform) float64 {
	const epsilon = 1e-6
	bounded := clamp(probability, epsilon, 1-epsilon)
	logOdds := math.Log(bounded / (1 - bounded))
	value := transform.Slope*logOdds + transform.Intercept
	if value >= 0 {
		return 1 / (1 + math.Exp(-value))
	}
	exponential := math.Exp(value)
	return exponential / (1 + exponential)
}

func mix(prior, transformed, weight float64) float64 {
	return prior*(1-weight) + transformed*weight
}

func clamp(value, minimum, maximum float64) float64 {
	return math.Min(math.Max(value, minimum), maximum)
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
