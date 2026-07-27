package router

import (
	"testing"

	"bouncer/internal/action"
	"bouncer/internal/learning"
)

func TestSelectLearnedHoldsNondominatedSet(t *testing.T) {
	predictions := []learning.Prediction{
		learnedPrediction("fast", 0.8, 0.7, 1, 3, 0.1, 0.01),
		learnedPrediction("cheap", 0.7, 0.8, 3, 1, 0.1, 0.01),
		learnedPrediction("dominated", 0.6, 0.6, 4, 4, 0.2, 0.01),
	}
	selection, err := SelectLearned(predictions, DefaultLearnedConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.FrontierCandidateIDs) != 2 {
		t.Fatalf("frontier = %v, want two candidates", selection.FrontierCandidateIDs)
	}
	for _, candidateID := range selection.FrontierCandidateIDs {
		if candidateID == "dominated" {
			t.Fatalf("dominated candidate entered frontier: %+v", selection)
		}
	}
	if selection.SelectionProbability != 1 {
		t.Fatalf("unexpected propensity: %+v", selection)
	}
}

func TestSelectLearnedAppliesRiskAndUncertaintyLimits(t *testing.T) {
	highRisk := learnedPrediction("risk", 1, 1, 1, 1, 0.8, 0.01)
	uncertain := learnedPrediction("uncertain", 1, 1, 1, 1, 0.1, 0.9)
	safe := learnedPrediction("safe", 0.5, 0.5, 2, 2, 0.1, 0.01)
	selection, err := SelectLearned([]learning.Prediction{highRisk, uncertain, safe}, LearnedConfig{
		RiskCeiling:            0.25,
		MaxRelativeUncertainty: 0.5,
		FrontierLimit:          16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Selected.Candidate.CandidateID != "safe" {
		t.Fatalf("selected %q", selection.Selected.Candidate.CandidateID)
	}
}

func learnedPrediction(
	id string,
	progress, success, latency, cost, risk, uncertainty float64,
) learning.Prediction {
	estimate := func(value float64) learning.Estimate {
		return learning.Estimate{Mean: value, Uncertainty: uncertainty, Conservative: value}
	}
	return learning.Prediction{
		Candidate: action.Candidate{
			CandidateID:          id,
			OperationClass:       "filesystem.read",
			Tool:                 "read_file",
			Target:               "workspace/" + id,
			Arguments:            map[string]any{},
			DeclaredDependencies: []string{},
			EstimatedObjectives:  action.Objectives{LatencyMS: latency, CostUnits: cost, SafetyRisk: risk},
		},
		Features:    map[string]float64{},
		Progress:    estimate(progress),
		Success:     estimate(success),
		LatencyMS:   estimate(latency),
		CostUnits:   estimate(cost),
		AdverseRisk: estimate(risk),
	}
}
