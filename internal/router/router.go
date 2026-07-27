package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"

	"bouncer/internal/action"
)

type NormalizedObjectives struct {
	LatencyMS  float64 `json:"latency_ms"`
	CostUnits  float64 `json:"cost_units"`
	SafetyRisk float64 `json:"safety_risk"`
}

type RankedCandidate struct {
	Candidate         action.Candidate     `json:"candidate"`
	RoutingObjectives action.Objectives    `json:"routing_objectives"`
	Rank              int                  `json:"rank"`
	Crowding          float64              `json:"crowding_distance"`
	Normalized        NormalizedObjectives `json:"normalized_objectives"`
}

type Selection struct {
	Selected                  action.Candidate  `json:"selected"`
	SelectedRoutingObjectives action.Objectives `json:"selected_routing_objectives"`
	Ranked                    []RankedCandidate `json:"ranked"`
	Strategy                  string            `json:"strategy"`
	SelectionProbability      float64           `json:"selection_probability"`
	SelectionScore            float64           `json:"selection_score"`
}

type Weights struct {
	LatencyMS  float64 `json:"latency_ms"`
	CostUnits  float64 `json:"cost_units"`
	SafetyRisk float64 `json:"safety_risk"`
}

type Config struct {
	Strategy    string  `json:"strategy"`
	RiskCeiling float64 `json:"risk_ceiling"`
	Weights     Weights `json:"weights"`
	RandomSeed  int64   `json:"random_seed"`
	Epsilon     float64 `json:"epsilon"`
}

const (
	StrategyLexicographic  = "lexicographic"
	StrategyWeighted       = "weighted_utility"
	StrategyParetoUtility  = "pareto_utility"
	StrategyRandomSafe     = "random_safe"
	StrategyEpsilonPareto  = "epsilon_pareto"
	StrategyLegacyCrowding = "legacy_crowding"
	StrategyFirstValid     = "first_valid"
)

func DefaultConfig() Config {
	return Config{
		Strategy:    StrategyLexicographic,
		RiskCeiling: 1,
		Weights: Weights{
			LatencyMS:  0.2,
			CostUnits:  0.2,
			SafetyRisk: 0.6,
		},
		Epsilon: 0.05,
	}
}

func Select(candidates []action.ScoredCandidate) (Selection, error) {
	return SelectWithConfig(candidates, DefaultConfig())
}

func SelectWithConfig(candidates []action.ScoredCandidate, config Config) (Selection, error) {
	config = withDefaults(config)
	if err := config.Validate(); err != nil {
		return Selection{}, err
	}
	eligible := make([]action.ScoredCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.RoutingObjectives.SafetyRisk <= config.RiskCeiling {
			eligible = append(eligible, candidate)
		}
	}
	if len(eligible) == 0 {
		return Selection{}, fmt.Errorf(
			"router found no candidate under risk ceiling %.4f",
			config.RiskCeiling,
		)
	}
	ranked, err := Rank(eligible)
	if err != nil {
		return Selection{}, err
	}
	ordered := append([]RankedCandidate(nil), ranked...)
	selection := Selection{
		Ranked:               ranked,
		Strategy:             config.Strategy,
		SelectionProbability: 1,
	}
	switch config.Strategy {
	case StrategyLexicographic:
		sort.SliceStable(ordered, func(i, j int) bool {
			return lessLexicographic(rankedScore(ordered[i]), rankedScore(ordered[j]))
		})
	case StrategyWeighted:
		sortByUtility(ordered, config.Weights)
		selection.SelectionScore = utility(ordered[0].Normalized, config.Weights)
	case StrategyParetoUtility:
		firstFront := ordered[:0]
		for _, candidate := range ordered {
			if candidate.Rank == 0 {
				firstFront = append(firstFront, candidate)
			}
		}
		ordered = firstFront
		sortByUtility(ordered, config.Weights)
		selection.SelectionScore = utility(ordered[0].Normalized, config.Weights)
	case StrategyRandomSafe:
		sort.SliceStable(ordered, func(i, j int) bool {
			return ordered[i].Candidate.CandidateID < ordered[j].Candidate.CandidateID
		})
		random := rand.New(rand.NewSource(config.RandomSeed))
		selected := random.Intn(len(ordered))
		ordered[0], ordered[selected] = ordered[selected], ordered[0]
		selection.SelectionProbability = 1 / float64(len(ordered))
	case StrategyEpsilonPareto:
		front := make([]RankedCandidate, 0, len(ordered))
		for _, candidate := range ordered {
			if candidate.Rank == 0 {
				front = append(front, candidate)
			}
		}
		sort.SliceStable(front, func(i, j int) bool {
			return lessLexicographic(rankedScore(front[i]), rankedScore(front[j]))
		})
		ordered = front
		if len(ordered) > 1 {
			random := rand.New(rand.NewSource(config.RandomSeed))
			if random.Float64() < config.Epsilon {
				selected := 1 + random.Intn(len(ordered)-1)
				ordered[0], ordered[selected] = ordered[selected], ordered[0]
				selection.SelectionProbability = config.Epsilon / float64(len(ordered)-1)
			} else {
				selection.SelectionProbability = 1 - config.Epsilon
			}
		}
	case StrategyLegacyCrowding:
		sort.SliceStable(ordered, func(i, j int) bool {
			if ordered[i].Rank != ordered[j].Rank {
				return ordered[i].Rank < ordered[j].Rank
			}
			if ordered[i].Crowding != ordered[j].Crowding {
				return ordered[i].Crowding > ordered[j].Crowding
			}
			return ordered[i].Candidate.CandidateID < ordered[j].Candidate.CandidateID
		})
	case StrategyFirstValid:
		selection.Selected = eligible[0].Candidate
		selection.SelectedRoutingObjectives = eligible[0].RoutingObjectives
		return selection, nil
	default:
		return Selection{}, fmt.Errorf("unsupported routing strategy %q", config.Strategy)
	}
	selection.Selected = ordered[0].Candidate
	selection.SelectedRoutingObjectives = ordered[0].RoutingObjectives
	return selection, nil
}

func (c Config) Validate() error {
	strategies := map[string]struct{}{
		StrategyLexicographic:  {},
		StrategyWeighted:       {},
		StrategyParetoUtility:  {},
		StrategyRandomSafe:     {},
		StrategyEpsilonPareto:  {},
		StrategyLegacyCrowding: {},
		StrategyFirstValid:     {},
	}
	if _, exists := strategies[c.Strategy]; !exists {
		return fmt.Errorf("unsupported routing strategy %q", c.Strategy)
	}
	if math.IsNaN(c.RiskCeiling) || math.IsInf(c.RiskCeiling, 0) ||
		c.RiskCeiling < 0 || c.RiskCeiling > 1 {
		return errors.New("routing risk ceiling must be between 0 and 1")
	}
	if math.IsNaN(c.Epsilon) || math.IsInf(c.Epsilon, 0) || c.Epsilon < 0 || c.Epsilon > 1 {
		return errors.New("routing epsilon must be between 0 and 1")
	}
	weights := []float64{c.Weights.LatencyMS, c.Weights.CostUnits, c.Weights.SafetyRisk}
	total := 0.0
	for _, weight := range weights {
		if math.IsNaN(weight) || math.IsInf(weight, 0) || weight < 0 {
			return errors.New("routing weights must be finite and non-negative")
		}
		total += weight
	}
	if (c.Strategy == StrategyWeighted || c.Strategy == StrategyParetoUtility) && total <= 0 {
		return errors.New("utility routing requires at least one positive weight")
	}
	return nil
}

func withDefaults(config Config) Config {
	defaults := DefaultConfig()
	if config == (Config{}) {
		return defaults
	}
	if strings.TrimSpace(config.Strategy) == "" {
		config.Strategy = defaults.Strategy
	}
	return config
}

// DiversitySpread returns the largest pairwise Euclidean distance after each
// objective is min-max normalized. The result is in [0, 1]; zero means that
// fewer than two distinct objective vectors were observed.
func DiversitySpread(candidates []action.ScoredCandidate) float64 {
	if len(candidates) < 2 {
		return 0
	}
	normalized := normalize(candidates)
	maximum := 0.0
	for left := range normalized {
		for right := left + 1; right < len(normalized); right++ {
			latency := normalized[left].LatencyMS - normalized[right].LatencyMS
			cost := normalized[left].CostUnits - normalized[right].CostUnits
			risk := normalized[left].SafetyRisk - normalized[right].SafetyRisk
			distance := math.Sqrt(latency*latency+cost*cost+risk*risk) / math.Sqrt(3)
			maximum = math.Max(maximum, distance)
		}
	}
	return maximum
}

func lessLexicographic(left, right action.ScoredCandidate) bool {
	leftObjectives := left.RoutingObjectives
	rightObjectives := right.RoutingObjectives
	if leftObjectives.SafetyRisk != rightObjectives.SafetyRisk {
		return leftObjectives.SafetyRisk < rightObjectives.SafetyRisk
	}
	if leftObjectives.CostUnits != rightObjectives.CostUnits {
		return leftObjectives.CostUnits < rightObjectives.CostUnits
	}
	if leftObjectives.LatencyMS != rightObjectives.LatencyMS {
		return leftObjectives.LatencyMS < rightObjectives.LatencyMS
	}
	return left.Candidate.CandidateID < right.Candidate.CandidateID
}

func sortByUtility(candidates []RankedCandidate, weights Weights) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := utility(candidates[i].Normalized, weights)
		right := utility(candidates[j].Normalized, weights)
		if left != right {
			return left < right
		}
		return lessLexicographic(rankedScore(candidates[i]), rankedScore(candidates[j]))
	})
}

func utility(objectives NormalizedObjectives, weights Weights) float64 {
	return objectives.LatencyMS*weights.LatencyMS +
		objectives.CostUnits*weights.CostUnits +
		objectives.SafetyRisk*weights.SafetyRisk
}

func Rank(candidates []action.ScoredCandidate) ([]RankedCandidate, error) {
	unique, err := Deduplicate(candidates)
	if err != nil {
		return nil, err
	}
	if len(unique) == 0 {
		return nil, errors.New("router requires at least one candidate")
	}
	for index := range unique {
		if err := unique[index].Validate(); err != nil {
			return nil, fmt.Errorf("candidate %d: %w", index, err)
		}
	}

	normalized := normalize(unique)
	ranks, fronts := nondominatedRanks(unique)
	crowding := make([]float64, len(unique))
	for _, front := range fronts {
		assignCrowding(front, normalized, unique, crowding)
	}

	ranked := make([]RankedCandidate, len(unique))
	for index := range unique {
		ranked[index] = RankedCandidate{
			Candidate:         unique[index].Candidate,
			RoutingObjectives: unique[index].RoutingObjectives,
			Rank:              ranks[index],
			Crowding:          crowding[index],
			Normalized:        normalized[index],
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].Candidate.CandidateID < ranked[j].Candidate.CandidateID
	})
	return ranked, nil
}

func Deduplicate(candidates []action.ScoredCandidate) ([]action.ScoredCandidate, error) {
	seen := make(map[string]struct{}, len(candidates))
	unique := make([]action.ScoredCandidate, 0, len(candidates))
	for index, candidate := range candidates {
		payload := struct {
			OperationClass       string         `json:"operation_class"`
			Tool                 string         `json:"tool"`
			Target               string         `json:"target"`
			Arguments            map[string]any `json:"arguments"`
			DeclaredDependencies []string       `json:"declared_dependencies"`
		}{
			OperationClass:       candidate.Candidate.OperationClass,
			Tool:                 candidate.Candidate.Tool,
			Target:               candidate.Candidate.Target,
			Arguments:            candidate.Candidate.Arguments,
			DeclaredDependencies: candidate.Candidate.DeclaredDependencies,
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("canonicalize candidate %d: %w", index, err)
		}
		key := string(encoded)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, candidate)
	}
	return unique, nil
}

func normalize(candidates []action.ScoredCandidate) []NormalizedObjectives {
	mins := [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}
	maxs := [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	values := make([][3]float64, len(candidates))
	for index, candidate := range candidates {
		values[index] = [3]float64{
			candidate.RoutingObjectives.LatencyMS,
			candidate.RoutingObjectives.CostUnits,
			candidate.RoutingObjectives.SafetyRisk,
		}
		for objective := range 3 {
			mins[objective] = math.Min(mins[objective], values[index][objective])
			maxs[objective] = math.Max(maxs[objective], values[index][objective])
		}
	}
	result := make([]NormalizedObjectives, len(candidates))
	for index := range candidates {
		normalized := [3]float64{}
		for objective := range 3 {
			span := maxs[objective] - mins[objective]
			if span > 0 {
				normalized[objective] = (values[index][objective] - mins[objective]) / span
			}
		}
		result[index] = NormalizedObjectives{
			LatencyMS:  normalized[0],
			CostUnits:  normalized[1],
			SafetyRisk: normalized[2],
		}
	}
	return result
}

func nondominatedRanks(candidates []action.ScoredCandidate) ([]int, [][]int) {
	dominatesList := make([][]int, len(candidates))
	dominatedByCount := make([]int, len(candidates))
	firstFront := make([]int, 0, len(candidates))
	for left := range candidates {
		for right := range candidates {
			if left == right {
				continue
			}
			if dominates(candidates[left].RoutingObjectives, candidates[right].RoutingObjectives) {
				dominatesList[left] = append(dominatesList[left], right)
			} else if dominates(candidates[right].RoutingObjectives, candidates[left].RoutingObjectives) {
				dominatedByCount[left]++
			}
		}
		if dominatedByCount[left] == 0 {
			firstFront = append(firstFront, left)
		}
	}

	ranks := make([]int, len(candidates))
	fronts := [][]int{firstFront}
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

func dominates(left, right action.Objectives) bool {
	leftValues := [3]float64{left.LatencyMS, left.CostUnits, left.SafetyRisk}
	rightValues := [3]float64{right.LatencyMS, right.CostUnits, right.SafetyRisk}
	strictlyBetter := false
	for objective := range 3 {
		if leftValues[objective] > rightValues[objective] {
			return false
		}
		if leftValues[objective] < rightValues[objective] {
			strictlyBetter = true
		}
	}
	return strictlyBetter
}

func assignCrowding(
	front []int,
	normalized []NormalizedObjectives,
	candidates []action.ScoredCandidate,
	distances []float64,
) {
	if len(front) == 0 {
		return
	}
	if len(front) == 1 {
		distances[front[0]] = math.MaxFloat64
		return
	}
	values := func(index, objective int) float64 {
		switch objective {
		case 0:
			return normalized[index].LatencyMS
		case 1:
			return normalized[index].CostUnits
		default:
			return normalized[index].SafetyRisk
		}
	}
	for objective := range 3 {
		ordered := append([]int(nil), front...)
		sort.SliceStable(ordered, func(i, j int) bool {
			left := values(ordered[i], objective)
			right := values(ordered[j], objective)
			if left != right {
				return left < right
			}
			return candidates[ordered[i]].Candidate.CandidateID < candidates[ordered[j]].Candidate.CandidateID
		})
		minimum := values(ordered[0], objective)
		maximum := values(ordered[len(ordered)-1], objective)
		if maximum == minimum {
			continue
		}
		distances[ordered[0]] = math.MaxFloat64
		distances[ordered[len(ordered)-1]] = math.MaxFloat64
		for position := 1; position < len(ordered)-1; position++ {
			index := ordered[position]
			if distances[index] == math.MaxFloat64 {
				continue
			}
			distances[index] += values(ordered[position+1], objective) - values(ordered[position-1], objective)
		}
	}
}

func rankedScore(candidate RankedCandidate) action.ScoredCandidate {
	return action.ScoredCandidate{
		Candidate:         candidate.Candidate,
		RoutingObjectives: candidate.RoutingObjectives,
	}
}
