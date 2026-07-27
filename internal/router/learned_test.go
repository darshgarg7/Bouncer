package router

import (
	"math"
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

func TestSelectLearnedCapsFrontierWithCrowdingDeterministically(t *testing.T) {
	predictions := make([]learning.Prediction, 0, 6)
	for index := 1; index <= 6; index++ {
		value := float64(index)
		predictions = append(predictions, learnedPrediction(
			string(rune('a'+index-1)),
			value/6,
			value/6,
			value,
			value,
			value/10,
			0.01,
		))
	}
	config := LearnedConfig{RiskCeiling: 1, MaxRelativeUncertainty: 1, FrontierLimit: 3}
	first, err := SelectLearned(predictions, config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SelectLearned(predictions, config)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.FrontierCandidateIDs) != 3 || first.Selected.Candidate.CandidateID != second.Selected.Candidate.CandidateID {
		t.Fatalf("unexpected deterministic cap: first=%+v second=%+v", first, second)
	}
}

func TestLearnedSelectionRejectsInvalidConfigurationPredictionsAndDuplicates(t *testing.T) {
	for name, config := range map[string]LearnedConfig{
		"risk":        {RiskCeiling: 2, MaxRelativeUncertainty: 1, FrontierLimit: 1},
		"uncertainty": {RiskCeiling: 1, MaxRelativeUncertainty: -1, FrontierLimit: 1},
		"frontier":    {RiskCeiling: 1, MaxRelativeUncertainty: 1, FrontierLimit: 0},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := SelectLearned([]learning.Prediction{learnedPrediction("a", 1, 1, 1, 1, 0, 0)}, config); err == nil {
				t.Fatal("invalid learned configuration was accepted")
			}
		})
	}
	base := learnedPrediction("a", 1, 1, 1, 1, 0, 0)
	for name, mutate := range map[string]func(*learning.Prediction){
		"candidate":   func(value *learning.Prediction) { value.Candidate.Tool = "" },
		"estimate":    func(value *learning.Prediction) { value.Progress.Mean = math.Inf(1) },
		"uncertainty": func(value *learning.Prediction) { value.Success.Uncertainty = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			value.Features = map[string]float64{}
			mutate(&value)
			if _, err := SelectLearned([]learning.Prediction{value}, DefaultLearnedConfig()); err == nil {
				t.Fatal("invalid prediction was accepted")
			}
		})
	}
	deduplicated, err := SelectLearned([]learning.Prediction{base, base}, DefaultLearnedConfig())
	if err != nil || len(deduplicated.Ranked) != 1 {
		t.Fatalf("duplicate prediction was not deterministically collapsed: %+v error=%v", deduplicated, err)
	}
	if _, err := SelectLearned(nil, DefaultLearnedConfig()); err == nil {
		t.Fatal("empty predictions were accepted")
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
