package learning

import (
	"context"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
)

const (
	// ArtifactSchemaVersion is the portable model artifact contract.
	ArtifactSchemaVersion = "0.1.0"
	// FeatureSchemaVersion identifies the exact ordered runtime feature set.
	FeatureSchemaVersion = "0.1.0"
)

// Context is the trusted state available to the scorer at one routing turn.
type Context struct {
	TaskID            string
	Turn              int
	MaxTurns          int
	State             benchmark.State
	Policy            benchmark.Policy
	PreviousOperation string
	RecentRejections  int
	NoProgressStreak  int
}

// Estimate contains a mean, an uncertainty in output units, and the
// conservative value used by the router.
type Estimate struct {
	Mean         float64 `json:"mean"`
	Uncertainty  float64 `json:"uncertainty"`
	Conservative float64 `json:"conservative"`
}

// Prediction contains independent outcome estimates for one candidate.
type Prediction struct {
	Candidate   action.Candidate   `json:"candidate"`
	Features    map[string]float64 `json:"features"`
	Progress    Estimate           `json:"progress"`
	Success     Estimate           `json:"success"`
	LatencyMS   Estimate           `json:"latency_ms"`
	CostUnits   Estimate           `json:"cost_units"`
	AdverseRisk Estimate           `json:"adverse_risk"`
}

// Batch is the immutable result of scoring a policy-admitted candidate set.
type Batch struct {
	Metadata    Metadata     `json:"metadata"`
	Predictions []Prediction `json:"predictions"`
}

// Metadata identifies the exact artifact used for a routing decision.
type Metadata struct {
	SchemaVersion        string     `json:"schema_version"`
	FeatureSchemaVersion string     `json:"feature_schema_version"`
	ArtifactID           string     `json:"artifact_id"`
	ArtifactSHA256       string     `json:"artifact_sha256"`
	Provenance           Provenance `json:"provenance"`
}

// Scorer is implemented by deterministic, immutable inference runtimes.
type Scorer interface {
	Score(context.Context, Context, []action.ScoredCandidate) (Batch, error)
	Metadata() Metadata
}
