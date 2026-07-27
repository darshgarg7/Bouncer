package anomaly

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
)

const (
	maximumTrees                   = 4096
	maximumSampleSize              = 1_000_000
	maximumTreeDepth               = 64
	maximumArtifactBytes           = 64 << 20
	minimumActiveValidationRows    = 20
	minimumActiveClassRows         = 5
	minimumActiveTruePositiveRate  = 0.80
	maximumActiveFalsePositiveRate = 0.05
)

// Runtime is an immutable, validated Isolation Forest ready for inference.
type Runtime struct {
	artifact Artifact
	metadata Metadata
}

// Load reads and strictly validates an artifact from disk. Its reported digest
// binds the exact file bytes, including formatting.
func Load(path string) (*Runtime, error) {
	data, err := readArtifact(path)
	if err != nil {
		return nil, fmt.Errorf("read anomaly artifact: %w", err)
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

// New validates and deep-copies an in-memory artifact.
func New(artifact Artifact) (*Runtime, error) {
	if err := artifact.Validate(); err != nil {
		return nil, fmt.Errorf("validate anomaly artifact: %w", err)
	}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		return nil, fmt.Errorf("encode anomaly artifact: %w", err)
	}
	var frozen Artifact
	if err := json.Unmarshal(encoded, &frozen); err != nil {
		return nil, fmt.Errorf("freeze anomaly artifact: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return &Runtime{
		artifact: frozen,
		metadata: metadataFor(frozen, hex.EncodeToString(digest[:])),
	}, nil
}

// Metadata returns a defensive copy of the runtime identity and provenance.
func (r *Runtime) Metadata() Metadata {
	if r == nil {
		return Metadata{}
	}
	return cloneMetadata(r.metadata)
}

// Validate rejects incompatible, ambiguous, non-finite, or excessively large
// artifacts before they can participate in runtime scoring.
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
	if len(a.FeatureNames) != len(featureNames) {
		return fmt.Errorf("feature_names must contain exactly %d entries", len(featureNames))
	}
	for index, expected := range featureNames {
		if a.FeatureNames[index] != expected {
			return fmt.Errorf("feature_names[%d] must be %q", index, expected)
		}
	}
	if err := validateProvenance(a.Provenance); err != nil {
		return err
	}
	if !finite(a.Threshold) || a.Threshold <= 0 || a.Threshold > 1 {
		return errors.New("threshold must be finite and in (0,1]")
	}
	if a.ActiveEligible && a.Provenance.Validation == nil {
		return errors.New("active_eligible requires labeled validation provenance")
	}
	if a.ActiveEligible && !passesActiveEligibility(*a.Provenance.Validation) {
		return errors.New("active_eligible validation does not pass the frozen eligibility gates")
	}
	if a.SampleSize < 2 || a.SampleSize > maximumSampleSize || a.SampleSize > a.Provenance.TrainingRows {
		return fmt.Errorf("sample_size must be between 2 and min(training_rows,%d)", maximumSampleSize)
	}
	if len(a.Trees) < 1 || len(a.Trees) > maximumTrees {
		return fmt.Errorf("trees must contain between 1 and %d roots", maximumTrees)
	}
	for index := range a.Trees {
		if a.Trees[index].Size != a.SampleSize {
			return fmt.Errorf("tree %d root size must equal sample_size", index)
		}
		nodes, err := validateNode(&a.Trees[index], 0)
		if err != nil {
			return fmt.Errorf("tree %d: %w", index, err)
		}
		if nodes > 2*a.SampleSize-1 {
			return fmt.Errorf("tree %d has too many nodes for sample_size", index)
		}
	}
	return nil
}

func passesActiveEligibility(validation ValidationProvenance) bool {
	return validation.Rows >= minimumActiveValidationRows &&
		validation.NormalRows >= minimumActiveClassRows &&
		validation.AnomalyRows >= minimumActiveClassRows &&
		validation.TruePositiveRate >= minimumActiveTruePositiveRate &&
		validation.FalsePositiveRate <= maximumActiveFalsePositiveRate
}

func validateProvenance(provenance Provenance) error {
	if strings.TrimSpace(provenance.Method) == "" {
		return errors.New("provenance method is required")
	}
	if !validSHA256(provenance.DatasetSHA256) {
		return errors.New("provenance dataset_sha256 must be lowercase SHA-256 hex")
	}
	if provenance.TrainingRows < 2 {
		return errors.New("provenance training_rows must be at least 2")
	}
	if provenance.Validation == nil {
		return nil
	}
	validation := provenance.Validation
	if !validSHA256(validation.DatasetSHA256) {
		return errors.New("validation dataset_sha256 must be lowercase SHA-256 hex")
	}
	if validation.DatasetSHA256 == provenance.DatasetSHA256 {
		return errors.New("training and validation dataset digests must differ")
	}
	if validation.Rows < 2 || validation.NormalRows < 1 || validation.AnomalyRows < 1 ||
		validation.NormalRows+validation.AnomalyRows != validation.Rows {
		return errors.New("validation rows must contain consistent nonempty normal and anomaly classes")
	}
	if !probability(validation.TruePositiveRate) || !probability(validation.FalsePositiveRate) {
		return errors.New("validation rates must be finite and between 0 and 1")
	}
	return nil
}

func validateNode(node *Node, depth int) (int, error) {
	if node == nil {
		return 0, errors.New("node is nil")
	}
	if depth > maximumTreeDepth {
		return 0, fmt.Errorf("depth exceeds %d", maximumTreeDepth)
	}
	if node.Size < 1 {
		return 0, errors.New("node size must be positive")
	}
	leaf := node.Feature == nil && node.Split == nil && node.Left == nil && node.Right == nil
	branch := node.Feature != nil && node.Split != nil && node.Left != nil && node.Right != nil
	if leaf {
		return 1, nil
	}
	if !branch {
		return 0, errors.New("node must be either a complete leaf or complete branch")
	}
	if *node.Feature < 0 || *node.Feature >= len(featureNames) {
		return 0, fmt.Errorf("feature index must be between 0 and %d", len(featureNames)-1)
	}
	if !finite(*node.Split) {
		return 0, errors.New("split must be finite")
	}
	if node.Size < 2 || node.Left.Size >= node.Size || node.Right.Size >= node.Size ||
		node.Left.Size+node.Right.Size != node.Size {
		return 0, errors.New("branch child sizes must sum to a branch size of at least 2")
	}
	leftNodes, err := validateNode(node.Left, depth+1)
	if err != nil {
		return 0, fmt.Errorf("left: %w", err)
	}
	rightNodes, err := validateNode(node.Right, depth+1)
	if err != nil {
		return 0, fmt.Errorf("right: %w", err)
	}
	return 1 + leftNodes + rightNodes, nil
}

func decodeStrict(data []byte) (Artifact, error) {
	if err := rejectDuplicateFields(data); err != nil {
		return Artifact{}, fmt.Errorf("decode anomaly artifact: %w", err)
	}
	var artifact Artifact
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&artifact); err != nil {
		return Artifact{}, fmt.Errorf("decode anomaly artifact: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Artifact{}, errors.New("decode anomaly artifact: trailing JSON value")
		}
		return Artifact{}, fmt.Errorf("decode anomaly artifact trailing content: %w", err)
	}
	return artifact, nil
}

func readArtifact(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maximumArtifactBytes {
		return nil, fmt.Errorf("artifact exceeds %d bytes", maximumArtifactBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maximumArtifactBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maximumArtifactBytes {
		return nil, fmt.Errorf("artifact exceeds %d bytes", maximumArtifactBytes)
	}
	return data, nil
}

func rejectDuplicateFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var visit func() error
	visit = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				field, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := field.(string)
				if !ok {
					return errors.New("object field is not a string")
				}
				if _, exists := seen[name]; exists {
					return fmt.Errorf("duplicate field %q", name)
				}
				seen[name] = struct{}{}
				if err := visit(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := visit(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected delimiter %q", delimiter)
		}
		_, err = decoder.Token()
		return err
	}
	if err := visit(); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func metadataFor(artifact Artifact, digest string) Metadata {
	return Metadata{
		SchemaVersion:        artifact.SchemaVersion,
		FeatureSchemaVersion: artifact.FeatureSchemaVersion,
		ArtifactID:           artifact.ArtifactID,
		ArtifactSHA256:       digest,
		CreatedAt:            artifact.CreatedAt,
		Provenance:           cloneProvenance(artifact.Provenance),
		Threshold:            artifact.Threshold,
		ActiveEligible:       artifact.ActiveEligible,
		SampleSize:           artifact.SampleSize,
		TreeCount:            len(artifact.Trees),
	}
}

func cloneMetadata(metadata Metadata) Metadata {
	metadata.Provenance = cloneProvenance(metadata.Provenance)
	return metadata
}

func cloneProvenance(provenance Provenance) Provenance {
	if provenance.Validation != nil {
		validation := *provenance.Validation
		provenance.Validation = &validation
	}
	return provenance
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func probability(value float64) bool {
	return finite(value) && value >= 0 && value <= 1
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
