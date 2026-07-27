package learning

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
	"time"
)

// Provenance records the immutable training-data boundary of an artifact.
type Provenance struct {
	Method         string `json:"method"`
	DatasetSHA256  string `json:"dataset_sha256"`
	TrainingRows   int    `json:"training_rows"`
	ValidationRows int    `json:"validation_rows"`
}

// LinearModel is a portable generalized-linear model.
type LinearModel struct {
	Link         string             `json:"link"`
	Intercept    float64            `json:"intercept"`
	Coefficients map[string]float64 `json:"coefficients"`
	Uncertainty  float64            `json:"uncertainty"`
}

// Models contains one independent model for every routing objective.
type Models struct {
	Progress    LinearModel `json:"progress"`
	Success     LinearModel `json:"success"`
	LatencyMS   LinearModel `json:"latency_ms"`
	CostUnits   LinearModel `json:"cost_units"`
	AdverseRisk LinearModel `json:"adverse_risk"`
}

// TransitionPrior is a bounded, smoothed workflow-sequence feature.
type TransitionPrior struct {
	FallbackProbability float64                       `json:"fallback_probability"`
	Probabilities       map[string]map[string]float64 `json:"probabilities"`
}

// Artifact is the complete portable inference document.
type Artifact struct {
	SchemaVersion        string          `json:"schema_version"`
	ArtifactID           string          `json:"artifact_id"`
	FeatureSchemaVersion string          `json:"feature_schema_version"`
	CreatedAt            time.Time       `json:"created_at"`
	Provenance           Provenance      `json:"provenance"`
	ConfidenceMultiplier float64         `json:"confidence_multiplier"`
	Models               Models          `json:"models"`
	TransitionPrior      TransitionPrior `json:"transition_prior"`
}

// Runtime is an immutable, validated learning artifact ready for inference.
type Runtime struct {
	artifact Artifact
	metadata Metadata
}

// Load reads and strictly validates an artifact from disk.
func Load(path string) (*Runtime, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read learning artifact: %w", err)
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

// New validates and freezes an in-memory artifact.
func New(artifact Artifact) (*Runtime, error) {
	if err := artifact.Validate(); err != nil {
		return nil, fmt.Errorf("validate learning artifact: %w", err)
	}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		return nil, fmt.Errorf("encode learning artifact: %w", err)
	}
	var frozen Artifact
	if err := json.Unmarshal(encoded, &frozen); err != nil {
		return nil, fmt.Errorf("freeze learning artifact: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return &Runtime{
		artifact: frozen,
		metadata: Metadata{
			SchemaVersion:        artifact.SchemaVersion,
			FeatureSchemaVersion: artifact.FeatureSchemaVersion,
			ArtifactID:           artifact.ArtifactID,
			ArtifactSHA256:       hex.EncodeToString(digest[:]),
			Provenance:           artifact.Provenance,
		},
	}, nil
}

// Metadata returns the identity and provenance of this immutable runtime.
func (r *Runtime) Metadata() Metadata {
	if r == nil {
		return Metadata{}
	}
	return r.metadata
}

// Validate rejects incompatible, ambiguous, or non-finite artifacts.
func (a Artifact) Validate() error {
	if a.SchemaVersion != ArtifactSchemaVersion {
		return fmt.Errorf("schema_version must be %s", ArtifactSchemaVersion)
	}
	if a.FeatureSchemaVersion != FeatureSchemaVersion {
		return fmt.Errorf("feature_schema_version must be %s", FeatureSchemaVersion)
	}
	if strings.TrimSpace(a.ArtifactID) == "" || len(a.ArtifactID) > 128 {
		return errors.New("artifact_id must contain 1 to 128 non-whitespace characters")
	}
	if a.CreatedAt.IsZero() {
		return errors.New("created_at is required")
	}
	if strings.TrimSpace(a.Provenance.Method) == "" {
		return errors.New("provenance method is required")
	}
	if !validSHA256(a.Provenance.DatasetSHA256) {
		return errors.New("provenance dataset_sha256 must be lowercase SHA-256 hex")
	}
	if a.Provenance.TrainingRows < 1 || a.Provenance.ValidationRows < 0 {
		return errors.New("provenance row counts are invalid")
	}
	if !finite(a.ConfidenceMultiplier) || a.ConfidenceMultiplier < 0 || a.ConfidenceMultiplier > 10 {
		return errors.New("confidence_multiplier must be finite and between 0 and 10")
	}
	models := []struct {
		name  string
		model LinearModel
	}{
		{name: "progress", model: a.Models.Progress},
		{name: "success", model: a.Models.Success},
		{name: "latency_ms", model: a.Models.LatencyMS},
		{name: "cost_units", model: a.Models.CostUnits},
		{name: "adverse_risk", model: a.Models.AdverseRisk},
	}
	for _, item := range models {
		if err := validateModel(item.model); err != nil {
			return fmt.Errorf("model %s: %w", item.name, err)
		}
	}
	if a.Models.Progress.Link != "logit" || a.Models.Success.Link != "logit" ||
		a.Models.AdverseRisk.Link != "logit" {
		return errors.New("progress, success, and adverse_risk models must use logit links")
	}
	if a.Models.LatencyMS.Link != "log1p" || a.Models.CostUnits.Link != "log1p" {
		return errors.New("latency_ms and cost_units models must use log1p links")
	}
	return validateTransitionPrior(a.TransitionPrior)
}

func validateModel(model LinearModel) error {
	if model.Link != "identity" && model.Link != "logit" && model.Link != "log1p" {
		return fmt.Errorf("unsupported link %q", model.Link)
	}
	if !finite(model.Intercept) || !finite(model.Uncertainty) || model.Uncertainty < 0 {
		return errors.New("intercept and non-negative uncertainty must be finite")
	}
	known := featureNameSet()
	for name, coefficient := range model.Coefficients {
		if _, ok := known[name]; !ok {
			return fmt.Errorf("unknown feature %q", name)
		}
		if !finite(coefficient) {
			return fmt.Errorf("coefficient %q must be finite", name)
		}
	}
	return nil
}

func validateTransitionPrior(prior TransitionPrior) error {
	if !finite(prior.FallbackProbability) || prior.FallbackProbability <= 0 || prior.FallbackProbability > 1 {
		return errors.New("transition fallback_probability must be in (0,1]")
	}
	for previous, next := range prior.Probabilities {
		if strings.TrimSpace(previous) == "" {
			return errors.New("transition prior contains an empty previous operation")
		}
		for operation, probability := range next {
			if strings.TrimSpace(operation) == "" || !finite(probability) || probability <= 0 || probability > 1 {
				return fmt.Errorf("transition %q -> %q has an invalid probability", previous, operation)
			}
		}
	}
	return nil
}

func decodeStrict(data []byte) (Artifact, error) {
	var artifact Artifact
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&artifact); err != nil {
		return Artifact{}, fmt.Errorf("decode learning artifact: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Artifact{}, errors.New("decode learning artifact: trailing JSON value")
		}
		return Artifact{}, fmt.Errorf("decode learning artifact trailing content: %w", err)
	}
	return artifact, nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
