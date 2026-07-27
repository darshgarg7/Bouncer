package router

import (
	"math"
	"testing"

	"bouncer/internal/action"
)

func TestRankAssignsNondominatedFronts(t *testing.T) {
	candidates := []action.Candidate{
		candidate("a", "workspace/a", 1, 1, 0.1),
		candidate("b", "workspace/b", 2, 2, 0.2),
		candidate("c", "workspace/c", 1, 3, 0.3),
	}
	ranked, err := Rank(candidates)
	if err != nil {
		t.Fatalf("Rank returned error: %v", err)
	}
	ranks := map[string]int{}
	for _, item := range ranked {
		ranks[item.Candidate.CandidateID] = item.Rank
	}
	if ranks["a"] != 0 || ranks["b"] != 1 || ranks["c"] != 1 {
		t.Fatalf("unexpected ranks: %v", ranks)
	}
}

func TestCrowdingPreservesBoundaryTradeoffs(t *testing.T) {
	candidates := []action.Candidate{
		candidate("fast", "workspace/fast", 1, 3, 0.3),
		candidate("middle", "workspace/middle", 2, 2, 0.2),
		candidate("safe", "workspace/safe", 3, 1, 0.1),
	}
	ranked, err := Rank(candidates)
	if err != nil {
		t.Fatalf("Rank returned error: %v", err)
	}
	distances := map[string]float64{}
	for _, item := range ranked {
		distances[item.Candidate.CandidateID] = item.Crowding
	}
	if distances["fast"] != math.MaxFloat64 || distances["safe"] != math.MaxFloat64 {
		t.Fatalf("boundary distances do not use the finite sentinel: %v", distances)
	}
	if distances["middle"] <= 0 || math.IsInf(distances["middle"], 0) {
		t.Fatalf("middle distance is invalid: %v", distances["middle"])
	}
}

func TestDeduplicateIgnoresCandidateID(t *testing.T) {
	first := candidate("agent-1", "workspace/file", 1, 1, 0.1)
	second := first
	second.CandidateID = "agent-2"
	unique, err := Deduplicate([]action.Candidate{first, second})
	if err != nil {
		t.Fatalf("Deduplicate returned error: %v", err)
	}
	if len(unique) != 1 || unique[0].CandidateID != "agent-1" {
		t.Fatalf("unexpected unique candidates: %+v", unique)
	}
}

func TestSelectUsesCandidateIDForExactTie(t *testing.T) {
	selection, err := Select([]action.Candidate{
		candidate("z", "workspace/z", 1, 1, 0.1),
		candidate("a", "workspace/a", 1, 1, 0.1),
	})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if selection.Selected.CandidateID != "a" {
		t.Fatalf("selected %q, want a", selection.Selected.CandidateID)
	}
}

func TestSelectRejectsEmptySet(t *testing.T) {
	if _, err := Select(nil); err == nil {
		t.Fatal("Select returned nil error for empty set")
	}
}

func TestSelectStrategiesHaveExplicitSemantics(t *testing.T) {
	candidates := []action.Candidate{
		candidate("fast-risky", "workspace/fast", 1, 1, 0.8),
		candidate("balanced", "workspace/balanced", 4, 4, 0.2),
		candidate("slow-safe", "workspace/safe", 8, 8, 0.1),
	}

	lexicographic, err := SelectWithConfig(candidates, Config{
		Strategy:    StrategyLexicographic,
		RiskCeiling: 1,
	})
	if err != nil {
		t.Fatalf("lexicographic selection: %v", err)
	}
	if lexicographic.Selected.CandidateID != "slow-safe" {
		t.Fatalf("lexicographic selected %q", lexicographic.Selected.CandidateID)
	}

	weighted, err := SelectWithConfig(candidates, Config{
		Strategy:    StrategyWeighted,
		RiskCeiling: 1,
		Weights: Weights{
			LatencyMS: 1,
		},
	})
	if err != nil {
		t.Fatalf("weighted selection: %v", err)
	}
	if weighted.Selected.CandidateID != "fast-risky" {
		t.Fatalf("weighted selected %q", weighted.Selected.CandidateID)
	}
}

func TestFirstValidPreservesProposalOrder(t *testing.T) {
	selection, err := SelectWithConfig([]action.Candidate{
		candidate("agent-2:candidate", "workspace/b", 1, 1, 0.9),
		candidate("agent-1:candidate", "workspace/a", 9, 9, 0.1),
	}, Config{Strategy: StrategyFirstValid, RiskCeiling: 1})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Selected.CandidateID != "agent-2:candidate" {
		t.Fatalf("selected %q", selection.Selected.CandidateID)
	}
}

func TestParetoUtilityExcludesDominatedCandidates(t *testing.T) {
	selection, err := SelectWithConfig([]action.Candidate{
		candidate("dominant", "workspace/a", 1, 1, 0.1),
		candidate("dominated", "workspace/b", 2, 2, 0.2),
	}, Config{
		Strategy:    StrategyParetoUtility,
		RiskCeiling: 1,
		Weights:     Weights{LatencyMS: 1, CostUnits: 1, SafetyRisk: 1},
	})
	if err != nil {
		t.Fatalf("SelectWithConfig returned error: %v", err)
	}
	if selection.Selected.CandidateID != "dominant" {
		t.Fatalf("selected %q", selection.Selected.CandidateID)
	}
}

func TestRandomSafeIsSeededAndReportsPropensity(t *testing.T) {
	candidates := []action.Candidate{
		candidate("a", "workspace/a", 1, 1, 0.1),
		candidate("b", "workspace/b", 2, 2, 0.2),
		candidate("c", "workspace/c", 3, 3, 0.3),
	}
	config := Config{Strategy: StrategyRandomSafe, RiskCeiling: 1, RandomSeed: 42}
	first, err := SelectWithConfig(candidates, config)
	if err != nil {
		t.Fatalf("first selection: %v", err)
	}
	second, err := SelectWithConfig(candidates, config)
	if err != nil {
		t.Fatalf("second selection: %v", err)
	}
	if first.Selected.CandidateID != second.Selected.CandidateID {
		t.Fatalf("seeded selections differ: %q and %q", first.Selected.CandidateID, second.Selected.CandidateID)
	}
	if first.SelectionProbability != 1.0/3.0 {
		t.Fatalf("selection probability %v, want %v", first.SelectionProbability, 1.0/3.0)
	}
}

func TestEpsilonParetoLogsExactBehaviorProbability(t *testing.T) {
	candidates := []action.Candidate{
		candidate("fast", "workspace/fast", 1, 3, 0.3),
		candidate("balanced", "workspace/balanced", 2, 2, 0.2),
		candidate("safe", "workspace/safe", 3, 1, 0.1),
		candidate("dominated", "workspace/dominated", 4, 4, 0.4),
	}
	optimal, err := SelectWithConfig(candidates, Config{
		Strategy:    StrategyEpsilonPareto,
		RiskCeiling: 1,
		Epsilon:     0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if optimal.Selected.CandidateID != "safe" || optimal.SelectionProbability != 1 {
		t.Fatalf("unexpected exploitation selection: %+v", optimal)
	}
	exploratory, err := SelectWithConfig(candidates, Config{
		Strategy:    StrategyEpsilonPareto,
		RiskCeiling: 1,
		Epsilon:     1,
		RandomSeed:  42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if exploratory.Selected.CandidateID == "safe" || exploratory.Selected.CandidateID == "dominated" {
		t.Fatalf("unexpected exploratory selection: %+v", exploratory)
	}
	if exploratory.SelectionProbability != 0.5 {
		t.Fatalf("exploration probability %v, want 0.5", exploratory.SelectionProbability)
	}
}

func TestRiskCeilingCanBeZero(t *testing.T) {
	selection, err := SelectWithConfig([]action.Candidate{
		candidate("zero", "workspace/zero", 2, 2, 0),
		candidate("nonzero", "workspace/nonzero", 1, 1, 0.01),
	}, Config{Strategy: StrategyLexicographic, RiskCeiling: 0})
	if err != nil {
		t.Fatalf("SelectWithConfig returned error: %v", err)
	}
	if selection.Selected.CandidateID != "zero" {
		t.Fatalf("selected %q", selection.Selected.CandidateID)
	}
}

func TestSelectRejectsInvalidConfiguration(t *testing.T) {
	candidates := []action.Candidate{candidate("a", "workspace/a", 1, 1, 0.1)}
	for name, config := range map[string]Config{
		"strategy": {Strategy: "unknown", RiskCeiling: 1},
		"ceiling":  {Strategy: StrategyLexicographic, RiskCeiling: 2},
		"weights":  {Strategy: StrategyWeighted, RiskCeiling: 1, Weights: Weights{}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := SelectWithConfig(candidates, config); err == nil {
				t.Fatal("SelectWithConfig returned nil error")
			}
		})
	}
}

func TestDiversitySpread(t *testing.T) {
	identical := []action.Candidate{
		candidate("a", "workspace/a", 1, 1, 0.1),
		candidate("b", "workspace/b", 1, 1, 0.1),
	}
	if spread := DiversitySpread(identical); spread != 0 {
		t.Fatalf("identical spread %v, want zero", spread)
	}
	diverse := []action.Candidate{
		candidate("a", "workspace/a", 0, 0, 0),
		candidate("b", "workspace/b", 1, 1, 1),
	}
	if spread := DiversitySpread(diverse); math.Abs(spread-1) > 1e-12 {
		t.Fatalf("diverse spread %v, want one", spread)
	}
}

func candidate(id, target string, latency, cost, risk float64) action.Candidate {
	return action.Candidate{
		CandidateID:          id,
		OperationClass:       "filesystem.read",
		Tool:                 "read_file",
		Target:               target,
		Arguments:            map[string]any{},
		DeclaredDependencies: []string{},
		EstimatedObjectives: action.Objectives{
			LatencyMS:  latency,
			CostUnits:  cost,
			SafetyRisk: risk,
		},
	}
}

func FuzzRankNeverPanics(f *testing.F) {
	f.Add(1.0, 2.0, 0.1)
	f.Add(0.0, 0.0, 1.0)
	f.Fuzz(func(t *testing.T, latency, cost, risk float64) {
		if math.IsNaN(latency) || math.IsInf(latency, 0) || latency < 0 {
			latency = 0
		}
		if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
			cost = 0
		}
		if math.IsNaN(risk) || math.IsInf(risk, 0) || risk < 0 || risk > 1 {
			risk = 0
		}
		_, _ = Rank([]action.Candidate{
			candidate("a", "workspace/a", latency, cost, risk),
			candidate("b", "workspace/b", cost, latency, 1-risk),
		})
	})
}
