package learning

import (
	"encoding/json"
	"math"
	"strings"

	"bouncer/internal/action"
)

var operationClasses = []string{
	"filesystem.read",
	"filesystem.write",
	"filesystem.delete",
	"state.validate",
	"state.backup",
	"command.run",
	"service.deploy",
	"task.complete",
}

var featureNames = func() []string {
	names := []string{
		"turn_fraction",
		"remaining_turn_fraction",
		"mutation_fraction",
		"remaining_mutation_fraction",
		"completed_operation_count",
		"file_count",
		"constraint_feedback_count",
		"recent_rejection_count",
		"no_progress_streak",
		"candidate_mutating",
		"dependency_count",
		"dependency_satisfaction_ratio",
		"argument_count",
		"argument_bytes_log1p",
		"target_depth",
		"calibrated_latency_log1p",
		"calibrated_cost_log1p",
		"calibrated_safety_risk",
		"mutation_budget_interaction",
		"risk_mutation_interaction",
		"transition_log_probability",
		"transition_unseen",
	}
	for _, operation := range operationClasses {
		names = append(names, "operation="+operation, "previous="+operation)
	}
	return names
}()

// FeatureNames returns the stable ordered feature schema.
func FeatureNames() []string {
	return append([]string(nil), featureNames...)
}

func featureNameSet() map[string]struct{} {
	result := make(map[string]struct{}, len(featureNames))
	for _, name := range featureNames {
		result[name] = struct{}{}
	}
	return result
}

func extractFeatures(
	context Context,
	candidate action.ScoredCandidate,
	prior TransitionPrior,
) map[string]float64 {
	features := make(map[string]float64, len(featureNames))
	for _, name := range featureNames {
		features[name] = 0
	}
	maxTurns := max(context.MaxTurns, 1)
	turn := min(max(context.Turn, 0), maxTurns)
	features["turn_fraction"] = float64(turn) / float64(maxTurns)
	features["remaining_turn_fraction"] = float64(max(maxTurns-turn, 0)) / float64(maxTurns)
	maxMutations := max(context.Policy.MaxMutations, 1)
	mutations := min(max(context.State.MutationCount, 0), maxMutations)
	features["mutation_fraction"] = float64(mutations) / float64(maxMutations)
	features["remaining_mutation_fraction"] = float64(max(maxMutations-mutations, 0)) / float64(maxMutations)
	features["completed_operation_count"] = boundedCount(len(context.State.CompletedOperations))
	features["file_count"] = boundedCount(len(context.State.Files))
	features["constraint_feedback_count"] = boundedCount(len(context.State.ConstraintFeedback))
	features["recent_rejection_count"] = boundedCount(context.RecentRejections)
	features["no_progress_streak"] = boundedCount(context.NoProgressStreak)
	mutating := isMutating(candidate.Candidate.OperationClass)
	features["candidate_mutating"] = boolFloat(mutating)
	features["dependency_count"] = boundedCount(len(candidate.Candidate.DeclaredDependencies))
	features["dependency_satisfaction_ratio"] = dependencyRatio(
		candidate.Candidate.DeclaredDependencies,
		context.State.CompletedOperations,
	)
	features["argument_count"] = boundedCount(len(candidate.Candidate.Arguments))
	if encoded, err := json.Marshal(candidate.Candidate.Arguments); err == nil {
		features["argument_bytes_log1p"] = math.Log1p(float64(min(len(encoded), 1<<20)))
	}
	features["target_depth"] = boundedCount(strings.Count(candidate.Candidate.Target, "/") + 1)
	features["calibrated_latency_log1p"] = math.Log1p(candidate.RoutingObjectives.LatencyMS)
	features["calibrated_cost_log1p"] = math.Log1p(candidate.RoutingObjectives.CostUnits)
	features["calibrated_safety_risk"] = candidate.RoutingObjectives.SafetyRisk
	features["mutation_budget_interaction"] = features["candidate_mutating"] * features["remaining_mutation_fraction"]
	features["risk_mutation_interaction"] = features["candidate_mutating"] * features["calibrated_safety_risk"]
	probability, seen := transitionProbability(prior, context.PreviousOperation, candidate.Candidate.OperationClass)
	features["transition_log_probability"] = math.Max(math.Log(probability), -20)
	features["transition_unseen"] = boolFloat(!seen)
	features["operation="+candidate.Candidate.OperationClass] = 1
	if context.PreviousOperation != "" {
		features["previous="+context.PreviousOperation] = 1
	}
	return features
}

func transitionProbability(prior TransitionPrior, previous, next string) (float64, bool) {
	if previous == "" {
		previous = "START"
	}
	if transitions, ok := prior.Probabilities[previous]; ok {
		if probability, found := transitions[next]; found {
			return probability, true
		}
	}
	return prior.FallbackProbability, false
}

func dependencyRatio(dependencies, completed []string) float64 {
	if len(dependencies) == 0 {
		return 1
	}
	set := make(map[string]struct{}, len(completed))
	for _, operation := range completed {
		set[operation] = struct{}{}
	}
	satisfied := 0
	for _, dependency := range dependencies {
		if _, ok := set[dependency]; ok {
			satisfied++
		}
	}
	return float64(satisfied) / float64(len(dependencies))
}

func isMutating(operation string) bool {
	return operation == "filesystem.write" || operation == "filesystem.delete" || operation == "service.deploy"
}

func boundedCount(value int) float64 {
	return math.Log1p(float64(max(value, 0)))
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
