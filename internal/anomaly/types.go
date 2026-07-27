package anomaly

import (
	"time"

	"bouncer/internal/monitoring"
)

const (
	// ArtifactSchemaVersion identifies the portable Isolation Forest document.
	ArtifactSchemaVersion = "0.1.0"
	// FeatureSchemaVersion identifies the exact ordered monitoring feature set.
	FeatureSchemaVersion = "0.1.0"
)

var featureNames = [...]string{
	"rejection_rate",
	"retry_rate",
	"no_progress_streak",
	"tool_switch_rate",
	"latency_delta_ms",
	"transition_nll",
}

// FeatureNames returns a copy of the frozen ordered feature contract.
func FeatureNames() []string {
	result := make([]string, len(featureNames))
	copy(result, featureNames[:])
	return result
}

// ValidationProvenance records the labeled holdout evidence used to qualify
// an artifact for active gating. It is evidence metadata, not authorization.
type ValidationProvenance struct {
	DatasetSHA256     string  `json:"dataset_sha256"`
	Rows              int     `json:"rows"`
	NormalRows        int     `json:"normal_rows"`
	AnomalyRows       int     `json:"anomaly_rows"`
	TruePositiveRate  float64 `json:"true_positive_rate"`
	FalsePositiveRate float64 `json:"false_positive_rate"`
}

// Provenance records the immutable training and optional validation boundary.
type Provenance struct {
	Method        string                `json:"method"`
	DatasetSHA256 string                `json:"dataset_sha256"`
	TrainingRows  int                   `json:"training_rows"`
	Seed          int64                 `json:"seed"`
	Validation    *ValidationProvenance `json:"validation,omitempty"`
}

// Node is one portable Isolation Forest tree node. A leaf has nil feature,
// split, left, and right fields; a branch has all four fields populated.
type Node struct {
	Size    int      `json:"size"`
	Feature *int     `json:"feature"`
	Split   *float64 `json:"split"`
	Left    *Node    `json:"left"`
	Right   *Node    `json:"right"`
}

// Artifact is the complete portable anomaly-scoring document.
type Artifact struct {
	SchemaVersion        string     `json:"schema_version"`
	ArtifactID           string     `json:"artifact_id"`
	FeatureSchemaVersion string     `json:"feature_schema_version"`
	FeatureNames         []string   `json:"feature_names"`
	CreatedAt            time.Time  `json:"created_at"`
	Provenance           Provenance `json:"provenance"`
	Threshold            float64    `json:"threshold"`
	ActiveEligible       bool       `json:"active_eligible"`
	SampleSize           int        `json:"sample_size"`
	Trees                []Node     `json:"trees"`
}

// Metadata identifies the exact immutable artifact used for an evaluation.
type Metadata struct {
	SchemaVersion        string     `json:"schema_version"`
	FeatureSchemaVersion string     `json:"feature_schema_version"`
	ArtifactID           string     `json:"artifact_id"`
	ArtifactSHA256       string     `json:"artifact_sha256"`
	CreatedAt            time.Time  `json:"created_at"`
	Provenance           Provenance `json:"provenance"`
	Threshold            float64    `json:"threshold"`
	ActiveEligible       bool       `json:"active_eligible"`
	SampleSize           int        `json:"sample_size"`
	TreeCount            int        `json:"tree_count"`
}

// Evaluation is one deterministic anomaly decision.
type Evaluation struct {
	Score     float64 `json:"score"`
	Threshold float64 `json:"threshold"`
	Alert     bool    `json:"alert"`
}

// Scorer evaluates the frozen monitoring feature contract.
type Scorer interface {
	Score(monitoring.Features) (Evaluation, error)
	Metadata() Metadata
}
