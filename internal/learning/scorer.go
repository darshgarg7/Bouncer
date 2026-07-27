package learning

import (
	"context"
	"errors"
	"fmt"
	"math"

	"bouncer/internal/action"
)

// Score performs deterministic generalized-linear inference for a safe set.
func (r *Runtime) Score(
	ctx context.Context,
	decisionContext Context,
	candidates []action.ScoredCandidate,
) (Batch, error) {
	if r == nil {
		return Batch{}, errors.New("learning runtime is nil")
	}
	if err := ctx.Err(); err != nil {
		return Batch{}, err
	}
	if len(candidates) == 0 {
		return Batch{}, errors.New("learning scorer requires at least one candidate")
	}
	if decisionContext.MaxTurns <= 0 || decisionContext.Turn < 0 {
		return Batch{}, errors.New("learning context has an invalid turn horizon")
	}
	batch := Batch{
		Metadata:    r.metadata,
		Predictions: make([]Prediction, 0, len(candidates)),
	}
	for index, candidate := range candidates {
		if err := candidate.Validate(); err != nil {
			return Batch{}, fmt.Errorf("candidate %d: %w", index, err)
		}
		features := extractFeatures(decisionContext, candidate, r.artifact.TransitionPrior)
		prediction := Prediction{
			Candidate:   candidate.Candidate,
			Features:    features,
			Progress:    r.estimate(r.artifact.Models.Progress, features, false),
			Success:     r.estimate(r.artifact.Models.Success, features, false),
			LatencyMS:   r.estimate(r.artifact.Models.LatencyMS, features, true),
			CostUnits:   r.estimate(r.artifact.Models.CostUnits, features, true),
			AdverseRisk: r.estimate(r.artifact.Models.AdverseRisk, features, true),
		}
		batch.Predictions = append(batch.Predictions, prediction)
	}
	return batch, nil
}

func (r *Runtime) estimate(model LinearModel, features map[string]float64, upper bool) Estimate {
	linear := model.Intercept
	for name, coefficient := range model.Coefficients {
		linear += coefficient * features[name]
	}
	mean := inverseLink(model.Link, linear)
	delta := r.artifact.ConfidenceMultiplier * model.Uncertainty
	conservative := mean - delta
	if upper {
		conservative = mean + delta
	}
	if model.Link == "logit" {
		mean = clamp(mean, 0, 1)
		conservative = clamp(conservative, 0, 1)
	} else if model.Link == "log1p" {
		mean = math.Max(mean, 0)
		conservative = math.Max(conservative, 0)
	}
	return Estimate{Mean: mean, Uncertainty: model.Uncertainty, Conservative: conservative}
}

func inverseLink(link string, value float64) float64 {
	switch link {
	case "logit":
		if value >= 0 {
			return 1 / (1 + math.Exp(-value))
		}
		exponential := math.Exp(value)
		return exponential / (1 + exponential)
	case "log1p":
		return math.Expm1(math.Min(value, 700))
	default:
		return value
	}
}

func clamp(value, minimum, maximum float64) float64 {
	return math.Min(math.Max(value, minimum), maximum)
}
