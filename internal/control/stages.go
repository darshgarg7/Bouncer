package control

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"bouncer/internal/benchmark"
	"bouncer/internal/calibration"
	"bouncer/internal/learning"
	"bouncer/internal/monitoring"
	"bouncer/internal/router"
)

type routingStageInput struct {
	Context           context.Context
	Result            *Result
	Task              benchmark.Task
	State             benchmark.State
	Turn              int
	Seed              int64
	BaselineConfig    router.Config
	LearningConfig    LearningConfig
	Candidates        calibration.Batch
	PreviousOperation string
	RecentRejections  int
	NoProgressStreak  int
}

type routingStageResult struct {
	Selection             router.Selection
	StateDigest           string
	SelectedTransitionNLL float64
}

// routeStage chooses only from the calibrated, policy-admitted set and records
// the complete baseline and optional learned-routing decision.
func (l Loop) routeStage(input routingStageInput) (routingStageResult, error) {
	turnConfig := input.BaselineConfig
	turnConfig.RandomSeed = input.Seed + int64(input.Turn)
	stageContext, span := otel.Tracer("bouncer/control").Start(
		input.Context,
		"candidate.route",
		trace.WithAttributes(attribute.String("bouncer.routing.strategy", turnConfig.Strategy)),
	)
	defer span.End()
	selection, err := router.SelectWithConfig(input.Candidates.Candidates, turnConfig)
	if err != nil {
		markSpanError(span, err)
		return routingStageResult{}, fmt.Errorf("turn %d route candidates: %w", input.Turn, err)
	}
	learningEvidence := map[string]any{"mode": input.LearningConfig.Mode}
	selectedTransitionNLL := 0.0
	if input.LearningConfig.Mode != LearningDisabled {
		learnedBatch, scoreErr := l.LearningScorer.Score(stageContext, learning.Context{
			TaskID:            input.Task.TaskID,
			Turn:              input.Turn,
			MaxTurns:          l.MaxTurns,
			State:             input.State,
			Policy:            input.Task.Policy,
			PreviousOperation: input.PreviousOperation,
			RecentRejections:  input.RecentRejections,
			NoProgressStreak:  input.NoProgressStreak,
		}, input.Candidates.Candidates)
		if scoreErr != nil {
			markSpanError(span, scoreErr)
			return routingStageResult{}, fmt.Errorf(
				"turn %d learned scoring: %w",
				input.Turn,
				scoreErr,
			)
		}
		learnedSelection, selectErr := router.SelectLearned(
			learnedBatch.Predictions,
			input.LearningConfig.Router,
		)
		if selectErr != nil {
			if input.LearningConfig.Mode == LearningActive {
				markSpanError(span, selectErr)
				return routingStageResult{}, fmt.Errorf(
					"turn %d learned routing: %w",
					input.Turn,
					selectErr,
				)
			}
			learningEvidence = map[string]any{
				"mode":                   input.LearningConfig.Mode,
				"metadata":               learnedBatch.Metadata,
				"predictions":            learningPredictionEvidence(learnedBatch.Predictions),
				"frontier_candidate_ids": []string{},
				"shadow_error":           selectErr.Error(),
				"router":                 input.LearningConfig.Router,
			}
		} else {
			learningEvidence = map[string]any{
				"mode":                   input.LearningConfig.Mode,
				"metadata":               learnedBatch.Metadata,
				"predictions":            learningPredictionEvidence(learnedBatch.Predictions),
				"frontier_candidate_ids": learnedSelection.FrontierCandidateIDs,
				"shadow_action_id":       learnedSelection.Selected.Candidate.CandidateID,
				"disagrees": learnedSelection.Selected.Candidate.CandidateID !=
					selection.Selected.CandidateID,
				"router": input.LearningConfig.Router,
			}
		}
		if input.LearningConfig.Mode == LearningActive && selectErr == nil {
			selectedObjectives, found := calibratedObjectives(
				input.Candidates.Candidates,
				learnedSelection.Selected.Candidate.CandidateID,
			)
			if !found {
				return routingStageResult{}, fmt.Errorf(
					"turn %d learned selection is outside calibrated safe set",
					input.Turn,
				)
			}
			selection.Selected = learnedSelection.Selected.Candidate
			selection.SelectedRoutingObjectives = selectedObjectives
			selection.Strategy = learnedSelection.Strategy
			selection.SelectionProbability = learnedSelection.SelectionProbability
			selection.SelectionScore = 0
			input.Result.RoutingStrategy = learnedSelection.Strategy
		}
		selectedTransitionNLL = predictionTransitionNLL(
			learnedBatch.Predictions,
			selection.Selected.CandidateID,
		)
	}
	stateDigest, err := stateSHA256(input.State)
	if err != nil {
		return routingStageResult{}, err
	}
	if err := l.record(stageContext, input.Result, TraceEvent{
		EventType: "candidate.selected",
		StepID:    input.Turn,
		Payload: map[string]any{
			"action_id":              selection.Selected.CandidateID,
			"objective_source":       "trusted_calibration_artifact",
			"objective_calibration":  l.Calibrator.Metadata(),
			"objective_records":      input.Candidates.Records,
			"routing_objectives":     selection.SelectedRoutingObjectives,
			"strategy":               selection.Strategy,
			"selection_probability":  selection.SelectionProbability,
			"selection_score":        selection.SelectionScore,
			"risk_ceiling":           turnConfig.RiskCeiling,
			"epsilon":                turnConfig.Epsilon,
			"weights":                turnConfig.Weights,
			"ranked":                 rankedEvidence(selection.Ranked),
			"decision_id":            fmt.Sprintf("%s:%d", input.Task.TaskID, input.Turn),
			"behavior_probability":   selection.SelectionProbability,
			"state":                  stateEvidence(input.State, stateDigest),
			"eligible_candidates":    candidateEvidence(input.Candidates.Candidates),
			"feature_schema_version": learning.FeatureSchemaVersion,
			"learning":               learningEvidence,
		},
	}); err != nil {
		markSpanError(span, err)
		return routingStageResult{}, err
	}
	return routingStageResult{
		Selection:             selection,
		StateDigest:           stateDigest,
		SelectedTransitionNLL: selectedTransitionNLL,
	}, nil
}

type executionStageInput struct {
	Context               context.Context
	Result                *Result
	Task                  benchmark.Task
	State                 *benchmark.State
	Turn                  int
	Selection             router.Selection
	StateBeforeDigest     string
	RejectedCandidates    int
	CandidateCount        int
	SelectedTransitionNLL float64
	Monitor               *monitoring.Tracker
}

type executionStageResult struct {
	Operation        string
	Rejections       int
	NoProgressStreak int
}

// executeStage applies one authorized action, observes the resulting state,
// updates deterministic monitoring, and records the transition evidence.
func (l Loop) executeStage(input executionStageInput) (executionStageResult, error) {
	stageContext, span := otel.Tracer("bouncer/control").Start(
		input.Context,
		"action.execute",
		trace.WithAttributes(
			attribute.String("bouncer.action.id", input.Selection.Selected.CandidateID),
			attribute.String("bouncer.operation", input.Selection.Selected.OperationClass),
		),
	)
	defer span.End()
	progressBefore := taskProgress(input.Task, *input.State)
	hazardBefore := input.State.HazardInjected
	started := time.Now()
	outcome, err := l.Executor.Execute(
		stageContext,
		input.State,
		input.Task.Policy,
		input.Selection.Selected,
	)
	if err != nil {
		markSpanError(span, err)
		return executionStageResult{}, fmt.Errorf(
			"turn %d execute %s: %w",
			input.Turn,
			input.Selection.Selected.CandidateID,
			err,
		)
	}
	latencyMS := float64(time.Since(started).Microseconds()) / 1000
	stateAfterDigest, err := stateSHA256(*input.State)
	if err != nil {
		return executionStageResult{}, err
	}
	progressAfter := taskProgress(input.Task, *input.State)
	progressDelta := progressAfter - progressBefore
	window, err := input.Monitor.Observe(monitoring.Observation{
		RejectedCandidates: input.RejectedCandidates,
		CandidateCount:     input.CandidateCount,
		ProgressDelta:      progressDelta,
		MutationCount:      input.State.MutationCount,
		MaxMutations:       input.Task.Policy.MaxMutations,
		Operation:          input.Selection.Selected.OperationClass,
		LatencyMS:          latencyMS,
		TransitionNLL:      input.SelectedTransitionNLL,
	})
	if err != nil {
		return executionStageResult{}, fmt.Errorf("turn %d monitor outcome: %w", input.Turn, err)
	}
	input.Result.MonitoringAlerts += len(window.RuleAlerts)
	input.Result.ExecutedActions++
	if err := l.record(stageContext, input.Result, TraceEvent{
		EventType: "execution.completed",
		StepID:    input.Turn,
		Payload: map[string]any{
			"action_id":           input.Selection.Selected.CandidateID,
			"operation":           input.Selection.Selected.OperationClass,
			"outcome":             outcome,
			"decision_id":         fmt.Sprintf("%s:%d", input.Task.TaskID, input.Turn),
			"state_before_sha256": input.StateBeforeDigest,
			"state_after_sha256":  stateAfterDigest,
			"latency_ms":          latencyMS,
			"cost_units":          nil,
			"cost_censored":       true,
			"progress_before":     progressBefore,
			"progress_after":      progressAfter,
			"progress_delta":      progressDelta,
			"adverse":             input.State.HazardInjected && !hazardBefore,
			"terminal":            input.State.TaskComplete,
			"censored":            false,
			"monitoring":          window,
		},
	}); err != nil {
		markSpanError(span, err)
		return executionStageResult{}, err
	}
	return executionStageResult{
		Operation:        input.Selection.Selected.OperationClass,
		Rejections:       input.RejectedCandidates,
		NoProgressStreak: window.Features.NoProgressStreak,
	}, nil
}
