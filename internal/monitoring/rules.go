package monitoring

import (
	"errors"
	"math"
	"sort"
)

const (
	AlertRejectionBurst    = "rejection_burst"
	AlertNoProgressLoop    = "no_progress_loop"
	AlertMutationExhausted = "mutation_budget_exhausted"
	AlertToolAlternation   = "tool_alternation"
)

// Config contains explicit alert thresholds. Zero values receive defaults.
type Config struct {
	RejectionThreshold   int `json:"rejection_threshold"`
	NoProgressThreshold  int `json:"no_progress_threshold"`
	AlternationThreshold int `json:"alternation_threshold"`
}

// Observation is one verified turn outcome.
type Observation struct {
	RejectedCandidates int
	CandidateCount     int
	ProgressDelta      float64
	MutationCount      int
	MaxMutations       int
	Operation          string
	LatencyMS          float64
	TransitionNLL      float64
}

// Features is the bounded rolling telemetry vector used by anomaly training.
type Features struct {
	RejectionRate    float64 `json:"rejection_rate"`
	RetryRate        float64 `json:"retry_rate"`
	NoProgressStreak int     `json:"no_progress_streak"`
	ToolSwitchRate   float64 `json:"tool_switch_rate"`
	LatencyDeltaMS   float64 `json:"latency_delta_ms"`
	TransitionNLL    float64 `json:"transition_nll"`
}

// Window is the explainable result of observing one turn.
type Window struct {
	Features   Features `json:"features"`
	RuleAlerts []string `json:"rule_alerts"`
}

// Tracker maintains the minimal bounded state required for runtime rules.
type Tracker struct {
	config            Config
	noProgressStreak  int
	previousOperation string
	operationHistory  []string
	previousLatencyMS float64
	observedLatency   bool
}

// New validates configuration and returns an empty tracker.
func New(config Config) (*Tracker, error) {
	if config.RejectionThreshold == 0 {
		config.RejectionThreshold = 8
	}
	if config.NoProgressThreshold == 0 {
		config.NoProgressThreshold = 3
	}
	if config.AlternationThreshold == 0 {
		config.AlternationThreshold = 4
	}
	if config.RejectionThreshold < 1 || config.NoProgressThreshold < 1 ||
		config.AlternationThreshold < 3 {
		return nil, errors.New("monitoring thresholds must be positive and alternation must be at least 3")
	}
	return &Tracker{config: config, operationHistory: make([]string, 0, 8)}, nil
}

// Observe updates the tracker from trusted telemetry and returns current alerts.
func (t *Tracker) Observe(observation Observation) (Window, error) {
	if t == nil {
		return Window{}, errors.New("monitoring tracker is nil")
	}
	if observation.RejectedCandidates < 0 || observation.CandidateCount < 0 ||
		observation.RejectedCandidates > observation.CandidateCount ||
		observation.MutationCount < 0 || observation.MaxMutations < 0 ||
		!finite(observation.ProgressDelta) || !finite(observation.LatencyMS) ||
		observation.LatencyMS < 0 || !finite(observation.TransitionNLL) ||
		observation.TransitionNLL < 0 {
		return Window{}, errors.New("monitoring observation is invalid")
	}
	if observation.ProgressDelta <= 0 {
		t.noProgressStreak++
	} else {
		t.noProgressStreak = 0
	}
	latencyDelta := 0.0
	if t.observedLatency {
		latencyDelta = observation.LatencyMS - t.previousLatencyMS
	}
	t.previousLatencyMS = observation.LatencyMS
	t.observedLatency = true
	if observation.Operation != "" {
		t.operationHistory = append(t.operationHistory, observation.Operation)
		if len(t.operationHistory) > 8 {
			t.operationHistory = append([]string(nil), t.operationHistory[len(t.operationHistory)-8:]...)
		}
	}
	switchRate := toolSwitchRate(t.operationHistory)
	features := Features{
		RejectionRate:    safeRate(observation.RejectedCandidates, observation.CandidateCount),
		RetryRate:        0,
		NoProgressStreak: t.noProgressStreak,
		ToolSwitchRate:   switchRate,
		LatencyDeltaMS:   latencyDelta,
		TransitionNLL:    observation.TransitionNLL,
	}
	alerts := make([]string, 0, 4)
	if observation.RejectedCandidates >= t.config.RejectionThreshold {
		alerts = append(alerts, AlertRejectionBurst)
	}
	if t.noProgressStreak >= t.config.NoProgressThreshold {
		alerts = append(alerts, AlertNoProgressLoop)
	}
	if observation.MaxMutations > 0 && observation.MutationCount >= observation.MaxMutations {
		alerts = append(alerts, AlertMutationExhausted)
	}
	if len(t.operationHistory) >= t.config.AlternationThreshold &&
		alternates(t.operationHistory[len(t.operationHistory)-t.config.AlternationThreshold:]) {
		alerts = append(alerts, AlertToolAlternation)
	}
	sort.Strings(alerts)
	t.previousOperation = observation.Operation
	return Window{Features: features, RuleAlerts: alerts}, nil
}

func safeRate(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return math.Min(math.Max(float64(numerator)/float64(denominator), 0), 1)
}

func toolSwitchRate(history []string) float64 {
	if len(history) < 2 {
		return 0
	}
	switches := 0
	for index := 1; index < len(history); index++ {
		if history[index] != history[index-1] {
			switches++
		}
	}
	return float64(switches) / float64(len(history)-1)
}

func alternates(history []string) bool {
	if len(history) < 3 || history[0] == history[1] {
		return false
	}
	for index := 2; index < len(history); index++ {
		if history[index] != history[index%2] {
			return false
		}
	}
	return true
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
