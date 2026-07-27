package policy

import (
	"context"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
)

type Violation struct {
	Code    string            `json:"code"`
	Details map[string]string `json:"details"`
}

type Result struct {
	ActionID   string      `json:"action_id"`
	Allowed    bool        `json:"allowed"`
	Projection string      `json:"projection"`
	Violations []Violation `json:"violations"`
}

type BatchEvaluator interface {
	Evaluate(context.Context, []action.Candidate, benchmark.State, benchmark.Policy) ([]Result, error)
}
