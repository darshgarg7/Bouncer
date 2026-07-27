package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"bouncer/internal/learning"
)

// LearnedConfig controls uncertainty-aware Pareto holding. It cannot broaden
// the policy-admitted candidate set supplied by the caller.
type LearnedConfig struct {
	RiskCeiling            float64 `json:"risk_ceiling"`
	MaxRelativeUncertainty float64 `json:"max_relative_uncertainty"`
	FrontierLimit          int     `json:"frontier_limit"`
}

// ConservativeObjectives is the five-objective vector used for dominance.
type ConservativeObjectives struct {
	Progress    float64 `json:"progress"`
	Success     float64 `json:"success"`
	LatencyMS   float64 `json:"latency_ms"`
	CostUnits   float64 `json:"cost_units"`
	AdverseRisk float64 `json:"adverse_risk"`
}

// LearnedRankedCandidate explains one candidate's learned Pareto position.
type LearnedRankedCandidate struct {
	Prediction learning.Prediction    `json:"prediction"`
	Objectives ConservativeObjectives `json:"conservative_objectives"`
	Rank       int                    `json:"rank"`
	Crowding   float64                `json:"crowding_distance"`
}

// LearnedSelection is an auditable deterministic decision from learned values.
type LearnedSelection struct {
	Selected             learning.Prediction      `json:"selected"`
	Ranked               []LearnedRankedCandidate `json:"ranked"`
	FrontierCandidateIDs []string                 `json:"frontier_candidate_ids"`
	Strategy             string                   `json:"strategy"`
	SelectionProbability float64                  `json:"selection_probability"`
}

// DefaultLearnedConfig returns conservative runtime defaults.
func DefaultLearnedConfig() LearnedConfig {
	return LearnedConfig{
		RiskCeiling:            0.25,
		MaxRelativeUncertainty: 0.5,
		FrontierLimit:          16,
	}
}

// Validate rejects ambiguous learned-routing limits.
func (c LearnedConfig) Validate() error {
	if !finiteNumber(c.RiskCeiling) || c.RiskCeiling < 0 || c.RiskCeiling > 1 {
		return errors.New("learned risk ceiling must be between 0 and 1")
	}
	if !finiteNumber(c.MaxRelativeUncertainty) || c.MaxRelativeUncertainty < 0 ||
		c.MaxRelativeUncertainty > 10 {
		return errors.New("maximum relative uncertainty must be between 0 and 10")
	}
	if c.FrontierLimit < 1 || c.FrontierLimit > 256 {
		return errors.New("learned frontier limit must be between 1 and 256")
	}
	return nil
}

// SelectLearned retains the uncertainty-adjusted nondominated set and applies
// a documented safety-first selector. Every input must already pass policy.
func SelectLearned(
	predictions []learning.Prediction,
	config LearnedConfig,
) (LearnedSelection, error) {
	if err := config.Validate(); err != nil {
		return LearnedSelection{}, err
	}
	unique, err := deduplicatePredictions(predictions)
	if err != nil {
		return LearnedSelection{}, err
	}
	eligible := make([]learning.Prediction, 0, len(unique))
	for _, prediction := range unique {
		if err := validatePrediction(prediction); err != nil {
			return LearnedSelection{}, err
		}
		if prediction.AdverseRisk.Conservative > config.RiskCeiling {
			continue
		}
		if predictionUncertainty(prediction) > config.MaxRelativeUncertainty {
			continue
		}
		eligible = append(eligible, prediction)
	}
	if len(eligible) == 0 {
		return LearnedSelection{}, errors.New("learned router found no candidate inside risk and uncertainty limits")
	}
	ranked := rankLearned(eligible)
	frontier := make([]LearnedRankedCandidate, 0, len(ranked))
	for _, candidate := range ranked {
		if candidate.Rank == 0 {
			frontier = append(frontier, candidate)
		}
	}
	if len(frontier) > config.FrontierLimit {
		sort.SliceStable(frontier, func(i, j int) bool {
			if frontier[i].Crowding != frontier[j].Crowding {
				return frontier[i].Crowding > frontier[j].Crowding
			}
			return frontier[i].Prediction.Candidate.CandidateID <
				frontier[j].Prediction.Candidate.CandidateID
		})
		frontier = frontier[:config.FrontierLimit]
	}
	sort.SliceStable(frontier, func(i, j int) bool {
		return lessLearnedSafetyFirst(frontier[i], frontier[j])
	})
	frontierIDs := make([]string, len(frontier))
	for index, candidate := range frontier {
		frontierIDs[index] = candidate.Prediction.Candidate.CandidateID
	}
	return LearnedSelection{
		Selected:             frontier[0].Prediction,
		Ranked:               ranked,
		FrontierCandidateIDs: frontierIDs,
		Strategy:             "learned_pareto_safety_first",
		SelectionProbability: 1,
	}, nil
}

func rankLearned(predictions []learning.Prediction) []LearnedRankedCandidate {
	objectives := make([]ConservativeObjectives, len(predictions))
	for index, prediction := range predictions {
		objectives[index] = conservativeObjectives(prediction)
	}
	ranks, fronts := learnedNondominatedRanks(objectives)
	crowding := learnedCrowding(objectives, fronts)
	ranked := make([]LearnedRankedCandidate, len(predictions))
	for index, prediction := range predictions {
		ranked[index] = LearnedRankedCandidate{
			Prediction: prediction,
			Objectives: objectives[index],
			Rank:       ranks[index],
			Crowding:   crowding[index],
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].Prediction.Candidate.CandidateID < ranked[j].Prediction.Candidate.CandidateID
	})
	return ranked
}

func learnedNondominatedRanks(objectives []ConservativeObjectives) ([]int, [][]int) {
	dominatesList := make([][]int, len(objectives))
	dominatedByCount := make([]int, len(objectives))
	first := make([]int, 0, len(objectives))
	for left := range objectives {
		for right := range objectives {
			if left == right {
				continue
			}
			if learnedDominates(objectives[left], objectives[right]) {
				dominatesList[left] = append(dominatesList[left], right)
			} else if learnedDominates(objectives[right], objectives[left]) {
				dominatedByCount[left]++
			}
		}
		if dominatedByCount[left] == 0 {
			first = append(first, left)
		}
	}
	ranks := make([]int, len(objectives))
	fronts := [][]int{first}
	for rank := 0; rank < len(fronts); rank++ {
		next := make([]int, 0)
		for _, candidate := range fronts[rank] {
			ranks[candidate] = rank
			for _, dominated := range dominatesList[candidate] {
				dominatedByCount[dominated]--
				if dominatedByCount[dominated] == 0 {
					next = append(next, dominated)
				}
			}
		}
		if len(next) > 0 {
			fronts = append(fronts, next)
		}
	}
	return ranks, fronts
}

func learnedDominates(left, right ConservativeObjectives) bool {
	leftValues := minimizationValues(left)
	rightValues := minimizationValues(right)
	strict := false
	for index := range leftValues {
		if leftValues[index] > rightValues[index] {
			return false
		}
		strict = strict || leftValues[index] < rightValues[index]
	}
	return strict
}

func learnedCrowding(objectives []ConservativeObjectives, fronts [][]int) []float64 {
	distances := make([]float64, len(objectives))
	for _, front := range fronts {
		if len(front) <= 2 {
			for _, index := range front {
				distances[index] = math.MaxFloat64
			}
			continue
		}
		for objective := 0; objective < 5; objective++ {
			ordered := append([]int(nil), front...)
			sort.SliceStable(ordered, func(i, j int) bool {
				left := minimizationValues(objectives[ordered[i]])[objective]
				right := minimizationValues(objectives[ordered[j]])[objective]
				if left != right {
					return left < right
				}
				return ordered[i] < ordered[j]
			})
			minimum := minimizationValues(objectives[ordered[0]])[objective]
			maximum := minimizationValues(objectives[ordered[len(ordered)-1]])[objective]
			if maximum == minimum {
				continue
			}
			distances[ordered[0]] = math.MaxFloat64
			distances[ordered[len(ordered)-1]] = math.MaxFloat64
			for position := 1; position < len(ordered)-1; position++ {
				if distances[ordered[position]] == math.MaxFloat64 {
					continue
				}
				previous := minimizationValues(objectives[ordered[position-1]])[objective]
				next := minimizationValues(objectives[ordered[position+1]])[objective]
				distances[ordered[position]] += (next - previous) / (maximum - minimum)
			}
		}
	}
	return distances
}

func lessLearnedSafetyFirst(left, right LearnedRankedCandidate) bool {
	if left.Objectives.AdverseRisk != right.Objectives.AdverseRisk {
		return left.Objectives.AdverseRisk < right.Objectives.AdverseRisk
	}
	if left.Objectives.Success != right.Objectives.Success {
		return left.Objectives.Success > right.Objectives.Success
	}
	if left.Objectives.Progress != right.Objectives.Progress {
		return left.Objectives.Progress > right.Objectives.Progress
	}
	if left.Objectives.CostUnits != right.Objectives.CostUnits {
		return left.Objectives.CostUnits < right.Objectives.CostUnits
	}
	if left.Objectives.LatencyMS != right.Objectives.LatencyMS {
		return left.Objectives.LatencyMS < right.Objectives.LatencyMS
	}
	return left.Prediction.Candidate.CandidateID < right.Prediction.Candidate.CandidateID
}

func conservativeObjectives(prediction learning.Prediction) ConservativeObjectives {
	return ConservativeObjectives{
		Progress:    prediction.Progress.Conservative,
		Success:     prediction.Success.Conservative,
		LatencyMS:   prediction.LatencyMS.Conservative,
		CostUnits:   prediction.CostUnits.Conservative,
		AdverseRisk: prediction.AdverseRisk.Conservative,
	}
}

func minimizationValues(objectives ConservativeObjectives) [5]float64 {
	return [5]float64{
		-objectives.Progress,
		-objectives.Success,
		objectives.LatencyMS,
		objectives.CostUnits,
		objectives.AdverseRisk,
	}
}

func predictionUncertainty(prediction learning.Prediction) float64 {
	values := []float64{
		prediction.Progress.Uncertainty,
		prediction.Success.Uncertainty,
		prediction.LatencyMS.Uncertainty / (math.Abs(prediction.LatencyMS.Mean) + 1),
		prediction.CostUnits.Uncertainty / (math.Abs(prediction.CostUnits.Mean) + 1),
		prediction.AdverseRisk.Uncertainty,
	}
	maximum := 0.0
	for _, value := range values {
		maximum = math.Max(maximum, value)
	}
	return maximum
}

func validatePrediction(prediction learning.Prediction) error {
	if err := prediction.Candidate.Validate(); err != nil {
		return fmt.Errorf("learned candidate: %w", err)
	}
	estimates := []struct {
		name     string
		estimate learning.Estimate
		bounded  bool
	}{
		{name: "progress", estimate: prediction.Progress, bounded: true},
		{name: "success", estimate: prediction.Success, bounded: true},
		{name: "latency_ms", estimate: prediction.LatencyMS},
		{name: "cost_units", estimate: prediction.CostUnits},
		{name: "adverse_risk", estimate: prediction.AdverseRisk, bounded: true},
	}
	for _, item := range estimates {
		values := []float64{item.estimate.Mean, item.estimate.Uncertainty, item.estimate.Conservative}
		for _, value := range values {
			if !finiteNumber(value) || value < 0 || (item.bounded && value > 1) {
				return fmt.Errorf("learned %s estimate is outside its valid range", item.name)
			}
		}
	}
	return nil
}

func deduplicatePredictions(predictions []learning.Prediction) ([]learning.Prediction, error) {
	seen := make(map[string]struct{}, len(predictions))
	unique := make([]learning.Prediction, 0, len(predictions))
	for index, prediction := range predictions {
		payload := struct {
			OperationClass       string         `json:"operation_class"`
			Tool                 string         `json:"tool"`
			Target               string         `json:"target"`
			Arguments            map[string]any `json:"arguments"`
			DeclaredDependencies []string       `json:"declared_dependencies"`
		}{
			OperationClass:       prediction.Candidate.OperationClass,
			Tool:                 prediction.Candidate.Tool,
			Target:               prediction.Candidate.Target,
			Arguments:            prediction.Candidate.Arguments,
			DeclaredDependencies: prediction.Candidate.DeclaredDependencies,
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("canonicalize learned candidate %d: %w", index, err)
		}
		key := string(encoded)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, prediction)
	}
	if len(unique) == 0 {
		return nil, errors.New("learned router requires at least one candidate")
	}
	return unique, nil
}

func finiteNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
