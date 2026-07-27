package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
	"bouncer/internal/calibration"
	"bouncer/internal/executor"
	"bouncer/internal/harness"
	"bouncer/internal/learning"
	"bouncer/internal/monitoring"
	"bouncer/internal/nimclient"
	"bouncer/internal/projector"
	"bouncer/internal/router"
)

type Loop struct {
	Coordinator      harness.Coordinator
	Projector        projector.BatchProjector
	Calibrator       calibration.Calibrator
	Executor         executor.Executor
	TraceSink        TraceSink
	RouterConfig     router.Config
	LearningScorer   learning.Scorer
	Learning         LearningConfig
	Monitoring       monitoring.Config
	AdaptiveProposal AdaptiveProposalConfig
	MaxTurns         int
}

const (
	LearningDisabled = "disabled"
	LearningShadow   = "shadow"
	LearningActive   = "active"
)

// LearningConfig controls promotion independently from the immutable model.
type LearningConfig struct {
	Mode   string               `json:"mode"`
	Router router.LearnedConfig `json:"router"`
}

func (c LearningConfig) withDefaults() LearningConfig {
	if c.Mode == "" {
		c.Mode = LearningDisabled
	}
	if c.Router == (router.LearnedConfig{}) {
		c.Router = router.DefaultLearnedConfig()
	}
	return c
}

func (c LearningConfig) validate(scorer learning.Scorer) error {
	if c.Mode != LearningDisabled && c.Mode != LearningShadow && c.Mode != LearningActive {
		return fmt.Errorf("learning mode must be disabled, shadow, or active")
	}
	if c.Mode != LearningDisabled && scorer == nil {
		return fmt.Errorf("learning mode %s requires a scorer", c.Mode)
	}
	if err := c.Router.Validate(); err != nil {
		return fmt.Errorf("learning router configuration: %w", err)
	}
	return nil
}

type AdaptiveProposalConfig struct {
	Enabled       bool    `json:"enabled"`
	InitialCount  int     `json:"initial_count"`
	MinimumValid  int     `json:"minimum_valid"`
	MinimumSpread float64 `json:"minimum_spread"`
}

type TraceSink interface {
	Append(context.Context, TraceEvent) error
}

type TraceEvent struct {
	EventType string         `json:"event_type"`
	StepID    int            `json:"step_id"`
	Payload   map[string]any `json:"payload"`
}

type Result struct {
	Condition            string               `json:"condition"`
	TaskID               string               `json:"task_id"`
	Seed                 int64                `json:"seed"`
	Passed               bool                 `json:"passed"`
	TaskComplete         bool                 `json:"task_complete"`
	OracleFailures       []string             `json:"oracle_failures"`
	Turns                int                  `json:"turns"`
	ModelCalls           int                  `json:"model_calls"`
	PromptTokens         int                  `json:"prompt_tokens"`
	CompletionTokens     int                  `json:"completion_tokens"`
	ReasoningTokens      int                  `json:"reasoning_tokens"`
	TotalTokens          int                  `json:"total_tokens"`
	GeneratedCandidates  int                  `json:"generated_candidates"`
	ConstraintRejections int                  `json:"constraint_rejections"`
	ExecutedActions      int                  `json:"executed_actions"`
	SevereMutations      int                  `json:"severe_mutations"`
	RoutingStrategy      string               `json:"routing_strategy"`
	LearningMode         string               `json:"learning_mode"`
	LearningArtifact     *learning.Metadata   `json:"learning_artifact,omitempty"`
	MonitoringAlerts     int                  `json:"monitoring_alerts"`
	ObjectiveCalibration calibration.Metadata `json:"objective_calibration"`
	AdaptiveProposals    bool                 `json:"adaptive_proposals"`
	ProposalExpansions   int                  `json:"proposal_expansions"`
	DurationMS           int64                `json:"duration_ms"`
	FinalState           benchmark.State      `json:"final_state"`
	Trace                []TraceEvent         `json:"trace"`
}

func (l Loop) Run(ctx context.Context, task benchmark.Task, seed int64) (Result, error) {
	ctx, runSpan := otel.Tracer("bouncer/control").Start(
		ctx,
		"bouncer.run",
		trace.WithAttributes(
			attribute.String("bouncer.task.id", task.TaskID),
			attribute.Int64("bouncer.seed", seed),
		),
	)
	defer runSpan.End()
	if l.Projector == nil {
		return Result{}, fmt.Errorf("control loop projector is required")
	}
	if l.MaxTurns <= 0 {
		return Result{}, fmt.Errorf("control loop max turns must be positive")
	}
	if l.Executor == nil {
		return Result{}, fmt.Errorf("control loop executor is required")
	}
	if l.Calibrator == nil {
		return Result{}, fmt.Errorf("control loop objective calibrator is required")
	}
	routerConfig := l.RouterConfig
	if routerConfig == (router.Config{}) {
		routerConfig = router.DefaultConfig()
	}
	if err := routerConfig.Validate(); err != nil {
		return Result{}, fmt.Errorf("control loop router configuration: %w", err)
	}
	learningConfig := l.Learning.withDefaults()
	if err := learningConfig.validate(l.LearningScorer); err != nil {
		return Result{}, err
	}
	monitor, err := monitoring.New(l.Monitoring)
	if err != nil {
		return Result{}, fmt.Errorf("control loop monitoring configuration: %w", err)
	}
	adaptive, err := l.adaptiveConfig()
	if err != nil {
		return Result{}, err
	}
	started := time.Now()
	state := task.NewState()
	policyJSON, err := json.Marshal(task.Policy)
	if err != nil {
		return Result{}, fmt.Errorf("encode task policy: %w", err)
	}
	result := Result{
		Condition:            "bouncer",
		TaskID:               task.TaskID,
		Seed:                 seed,
		OracleFailures:       []string{},
		RoutingStrategy:      routerConfig.Strategy,
		LearningMode:         learningConfig.Mode,
		ObjectiveCalibration: l.Calibrator.Metadata(),
		AdaptiveProposals:    adaptive.Enabled,
		FinalState:           state,
		Trace:                []TraceEvent{},
	}
	if l.LearningScorer != nil {
		metadata := l.LearningScorer.Metadata()
		result.LearningArtifact = &metadata
	}
	previousOperation := ""
	recentRejections := 0
	noProgressStreak := 0

	for turn := 0; turn < l.MaxTurns && !state.TaskComplete; turn++ {
		// A turn starts from a complete typed snapshot. Provider output cannot
		// mutate state until it has passed projection and routing below.
		stateJSON, err := state.JSON()
		if err != nil {
			return Result{}, err
		}
		request := harness.Request{
			TaskID:      task.TaskID,
			Instruction: task.Instruction,
			State:       stateJSON,
			Policy:      policyJSON,
			BaseSeed:    seed + int64(turn*l.Coordinator.ProposerCount),
		}
		initialCount := l.Coordinator.ProposerCount
		if adaptive.Enabled {
			initialCount = adaptive.InitialCount
		}
		proposalContext, proposalSpan := otel.Tracer("bouncer/control").Start(
			ctx,
			"proposal.batch",
			trace.WithAttributes(attribute.Int("bouncer.proposer.count", initialCount)),
		)
		proposals, err := l.Coordinator.ProposeRange(proposalContext, request, 0, initialCount)
		if err != nil {
			markSpanError(proposalSpan, err)
			proposalSpan.End()
			return Result{}, fmt.Errorf("turn %d proposal round: %w", turn, err)
		}
		result.Turns++
		result.ModelCalls += len(proposals)
		candidates := flattenProposals(proposals, &result)
		turnCandidateCount := len(candidates)
		rejectionsBeforeTurn := result.ConstraintRejections
		if err := l.record(proposalContext, &result, TraceEvent{
			EventType: "proposal.completed",
			StepID:    turn,
			Payload: map[string]any{
				"proposal_count":    len(proposals),
				"candidate_count":   len(candidates),
				"provider_evidence": proposalEvidence(proposals),
			},
		}); err != nil {
			markSpanError(proposalSpan, err)
			proposalSpan.End()
			return Result{}, err
		}
		proposalSpan.End()

		valid, feedback, err := l.project(ctx, &result, turn, candidates, state, task.Policy)
		if err != nil {
			return Result{}, err
		}
		routingBatch := calibration.Batch{}
		if len(valid) > 0 {
			routingBatch, err = l.Calibrator.Calibrate(valid)
			if err != nil {
				return Result{}, fmt.Errorf("turn %d calibrate objectives: %w", turn, err)
			}
		}
		if adaptive.Enabled && initialCount < l.Coordinator.ProposerCount {
			spread := router.DiversitySpread(routingBatch.Candidates)
			reasons := expansionReasons(valid, spread, adaptive)
			if len(reasons) > 0 {
				remaining := l.Coordinator.ProposerCount - initialCount
				extra, proposalErr := l.Coordinator.ProposeRange(ctx, request, initialCount, remaining)
				if proposalErr != nil {
					return Result{}, fmt.Errorf("turn %d expanded proposal round: %w", turn, proposalErr)
				}
				result.ProposalExpansions++
				result.ModelCalls += len(extra)
				extraCandidates := flattenProposals(extra, &result)
				turnCandidateCount += len(extraCandidates)
				if err := l.record(ctx, &result, TraceEvent{
					EventType: "proposal.expanded",
					StepID:    turn,
					Payload: map[string]any{
						"reasons":              reasons,
						"initial_proposers":    initialCount,
						"additional_proposers": remaining,
						"valid_before":         len(valid),
						"spread_before":        spread,
					},
				}); err != nil {
					return Result{}, err
				}
				extraValid, extraFeedback, projectErr := l.project(
					ctx,
					&result,
					turn,
					extraCandidates,
					state,
					task.Policy,
				)
				if projectErr != nil {
					return Result{}, projectErr
				}
				valid = append(valid, extraValid...)
				feedback = append(feedback, extraFeedback...)
				if len(valid) > 0 {
					routingBatch, err = l.Calibrator.Calibrate(valid)
					if err != nil {
						return Result{}, fmt.Errorf("turn %d calibrate expanded objectives: %w", turn, err)
					}
				}
			}
		}
		if len(valid) == 0 {
			// Rejections become input to the next proposal turn; they are never
			// converted into a best-effort execution fallback.
			sort.Strings(feedback)
			state.ConstraintFeedback = feedback
			recentRejections = result.ConstraintRejections - rejectionsBeforeTurn
			noProgressStreak++
			continue
		}
		turnRouterConfig := routerConfig
		turnRouterConfig.RandomSeed = seed + int64(turn)
		routingContext, routingSpan := otel.Tracer("bouncer/control").Start(
			ctx,
			"candidate.route",
			trace.WithAttributes(attribute.String("bouncer.routing.strategy", turnRouterConfig.Strategy)),
		)
		selection, err := router.SelectWithConfig(routingBatch.Candidates, turnRouterConfig)
		if err != nil {
			markSpanError(routingSpan, err)
			routingSpan.End()
			return Result{}, fmt.Errorf("turn %d route candidates: %w", turn, err)
		}
		learningEvidence := map[string]any{"mode": learningConfig.Mode}
		selectedTransitionNLL := 0.0
		if learningConfig.Mode != LearningDisabled {
			learnedBatch, scoreErr := l.LearningScorer.Score(routingContext, learning.Context{
				TaskID:            task.TaskID,
				Turn:              turn,
				MaxTurns:          l.MaxTurns,
				State:             state,
				Policy:            task.Policy,
				PreviousOperation: previousOperation,
				RecentRejections:  recentRejections,
				NoProgressStreak:  noProgressStreak,
			}, routingBatch.Candidates)
			if scoreErr != nil {
				markSpanError(routingSpan, scoreErr)
				routingSpan.End()
				return Result{}, fmt.Errorf("turn %d learned scoring: %w", turn, scoreErr)
			}
			learnedSelection, selectErr := router.SelectLearned(
				learnedBatch.Predictions,
				learningConfig.Router,
			)
			if selectErr != nil {
				if learningConfig.Mode == LearningActive {
					markSpanError(routingSpan, selectErr)
					routingSpan.End()
					return Result{}, fmt.Errorf("turn %d learned routing: %w", turn, selectErr)
				}
				learningEvidence = map[string]any{
					"mode":                   learningConfig.Mode,
					"metadata":               learnedBatch.Metadata,
					"predictions":            learningPredictionEvidence(learnedBatch.Predictions),
					"frontier_candidate_ids": []string{},
					"shadow_error":           selectErr.Error(),
					"router":                 learningConfig.Router,
				}
			} else {
				learningEvidence = map[string]any{
					"mode":                   learningConfig.Mode,
					"metadata":               learnedBatch.Metadata,
					"predictions":            learningPredictionEvidence(learnedBatch.Predictions),
					"frontier_candidate_ids": learnedSelection.FrontierCandidateIDs,
					"shadow_action_id":       learnedSelection.Selected.Candidate.CandidateID,
					"disagrees":              learnedSelection.Selected.Candidate.CandidateID != selection.Selected.CandidateID,
					"router":                 learningConfig.Router,
				}
			}
			if learningConfig.Mode == LearningActive && selectErr == nil {
				selectedObjectives, found := calibratedObjectives(
					routingBatch.Candidates,
					learnedSelection.Selected.Candidate.CandidateID,
				)
				if !found {
					routingSpan.End()
					return Result{}, fmt.Errorf("turn %d learned selection is outside calibrated safe set", turn)
				}
				selection.Selected = learnedSelection.Selected.Candidate
				selection.SelectedRoutingObjectives = selectedObjectives
				selection.Strategy = learnedSelection.Strategy
				selection.SelectionProbability = learnedSelection.SelectionProbability
				selection.SelectionScore = 0
				result.RoutingStrategy = learnedSelection.Strategy
			}
			selectedTransitionNLL = predictionTransitionNLL(
				learnedBatch.Predictions,
				selection.Selected.CandidateID,
			)
		}
		stateDigest, err := stateSHA256(state)
		if err != nil {
			routingSpan.End()
			return Result{}, err
		}
		if err := l.record(routingContext, &result, TraceEvent{
			EventType: "candidate.selected",
			StepID:    turn,
			Payload: map[string]any{
				"action_id":              selection.Selected.CandidateID,
				"objective_source":       "trusted_calibration_artifact",
				"objective_calibration":  l.Calibrator.Metadata(),
				"objective_records":      routingBatch.Records,
				"routing_objectives":     selection.SelectedRoutingObjectives,
				"strategy":               selection.Strategy,
				"selection_probability":  selection.SelectionProbability,
				"selection_score":        selection.SelectionScore,
				"risk_ceiling":           turnRouterConfig.RiskCeiling,
				"epsilon":                turnRouterConfig.Epsilon,
				"weights":                turnRouterConfig.Weights,
				"ranked":                 rankedEvidence(selection.Ranked),
				"decision_id":            fmt.Sprintf("%s:%d", task.TaskID, turn),
				"behavior_probability":   selection.SelectionProbability,
				"state":                  stateEvidence(state, stateDigest),
				"eligible_candidates":    candidateEvidence(routingBatch.Candidates),
				"feature_schema_version": learning.FeatureSchemaVersion,
				"learning":               learningEvidence,
			},
		}); err != nil {
			markSpanError(routingSpan, err)
			routingSpan.End()
			return Result{}, err
		}
		routingSpan.End()
		executionContext, executionSpan := otel.Tracer("bouncer/control").Start(
			ctx,
			"action.execute",
			trace.WithAttributes(
				attribute.String("bouncer.action.id", selection.Selected.CandidateID),
				attribute.String("bouncer.operation", selection.Selected.OperationClass),
			),
		)
		progressBefore := taskProgress(task, state)
		stateBeforeDigest := stateDigest
		hazardBefore := state.HazardInjected
		executionStarted := time.Now()
		outcome, err := l.Executor.Execute(executionContext, &state, task.Policy, selection.Selected)
		if err != nil {
			markSpanError(executionSpan, err)
			executionSpan.End()
			return Result{}, fmt.Errorf("turn %d execute %s: %w", turn, selection.Selected.CandidateID, err)
		}
		executionLatencyMS := float64(time.Since(executionStarted).Microseconds()) / 1000
		stateAfterDigest, err := stateSHA256(state)
		if err != nil {
			executionSpan.End()
			return Result{}, err
		}
		progressAfter := taskProgress(task, state)
		progressDelta := progressAfter - progressBefore
		window, monitorErr := monitor.Observe(monitoring.Observation{
			RejectedCandidates: result.ConstraintRejections - rejectionsBeforeTurn,
			CandidateCount:     turnCandidateCount,
			ProgressDelta:      progressDelta,
			MutationCount:      state.MutationCount,
			MaxMutations:       task.Policy.MaxMutations,
			Operation:          selection.Selected.OperationClass,
			LatencyMS:          executionLatencyMS,
			TransitionNLL:      selectedTransitionNLL,
		})
		if monitorErr != nil {
			executionSpan.End()
			return Result{}, fmt.Errorf("turn %d monitor outcome: %w", turn, monitorErr)
		}
		result.MonitoringAlerts += len(window.RuleAlerts)
		result.ExecutedActions++
		if err := l.record(executionContext, &result, TraceEvent{
			EventType: "execution.completed",
			StepID:    turn,
			Payload: map[string]any{
				"action_id":           selection.Selected.CandidateID,
				"operation":           selection.Selected.OperationClass,
				"outcome":             outcome,
				"decision_id":         fmt.Sprintf("%s:%d", task.TaskID, turn),
				"state_before_sha256": stateBeforeDigest,
				"state_after_sha256":  stateAfterDigest,
				"latency_ms":          executionLatencyMS,
				"cost_units":          nil,
				"cost_censored":       true,
				"progress_before":     progressBefore,
				"progress_after":      progressAfter,
				"progress_delta":      progressDelta,
				"adverse":             state.HazardInjected && !hazardBefore,
				"terminal":            state.TaskComplete,
				"censored":            false,
				"monitoring":          window,
			},
		}); err != nil {
			markSpanError(executionSpan, err)
			executionSpan.End()
			return Result{}, err
		}
		executionSpan.End()
		previousOperation = selection.Selected.OperationClass
		recentRejections = result.ConstraintRejections - rejectionsBeforeTurn
		noProgressStreak = window.Features.NoProgressStreak
	}

	oracle := task.Evaluate(state)
	result.Passed = oracle.Passed && state.TaskComplete
	result.TaskComplete = state.TaskComplete
	result.OracleFailures = oracle.Failures
	if oracle.Passed && !state.TaskComplete {
		result.OracleFailures = append(result.OracleFailures, "task did not emit task.complete")
	}
	result.FinalState = state
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

func proposalEvidence(proposals []nimclient.ProposalResult) []map[string]any {
	evidence := make([]map[string]any, 0, len(proposals))
	for _, proposal := range proposals {
		evidence = append(evidence, map[string]any{
			"proposer_id":         proposal.ProposerID,
			"provider_request_id": proposal.ProviderRequestID,
			"model":               proposal.Model,
			"request_hash":        proposal.RequestHash,
			"response_hash":       proposal.ResponseHash,
			"finish_reason":       proposal.FinishReason,
			"latency_ms":          proposal.LatencyMS,
			"attempts":            proposal.Attempts,
			"usage":               proposal.Usage,
		})
	}
	return evidence
}

func calibratedObjectives(
	candidates []action.ScoredCandidate,
	candidateID string,
) (action.Objectives, bool) {
	for _, candidate := range candidates {
		if candidate.Candidate.CandidateID == candidateID {
			return candidate.RoutingObjectives, true
		}
	}
	return action.Objectives{}, false
}

func stateSHA256(state benchmark.State) (string, error) {
	encoded, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("encode state for digest: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func stateEvidence(state benchmark.State, digest string) map[string]any {
	return map[string]any{
		"state_sha256":              digest,
		"benchmark_step":            state.BenchmarkStep,
		"mutation_count":            state.MutationCount,
		"completed_operations":      append([]string(nil), state.CompletedOperations...),
		"file_count":                len(state.Files),
		"constraint_feedback_count": len(state.ConstraintFeedback),
	}
}

func candidateEvidence(candidates []action.ScoredCandidate) []map[string]any {
	evidence := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		evidence = append(evidence, map[string]any{
			"candidate_id":       candidate.Candidate.CandidateID,
			"operation_class":    candidate.Candidate.OperationClass,
			"tool":               candidate.Candidate.Tool,
			"target_category":    targetCategory(candidate.Candidate.Target),
			"routing_objectives": candidate.RoutingObjectives,
		})
	}
	return evidence
}

func rankedEvidence(candidates []router.RankedCandidate) []map[string]any {
	evidence := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		evidence = append(evidence, map[string]any{
			"candidate_id":          candidate.Candidate.CandidateID,
			"routing_objectives":    candidate.RoutingObjectives,
			"rank":                  candidate.Rank,
			"crowding_distance":     candidate.Crowding,
			"normalized_objectives": candidate.Normalized,
		})
	}
	return evidence
}

func learningPredictionEvidence(predictions []learning.Prediction) []map[string]any {
	evidence := make([]map[string]any, 0, len(predictions))
	for _, prediction := range predictions {
		evidence = append(evidence, map[string]any{
			"candidate_id": prediction.Candidate.CandidateID,
			"features":     prediction.Features,
			"progress":     prediction.Progress,
			"success":      prediction.Success,
			"latency_ms":   prediction.LatencyMS,
			"cost_units":   prediction.CostUnits,
			"adverse_risk": prediction.AdverseRisk,
		})
	}
	return evidence
}

func targetCategory(target string) string {
	trimmed := strings.Trim(target, "/")
	if trimmed == "" {
		return "unknown"
	}
	category, _, _ := strings.Cut(trimmed, "/")
	return category
}

func taskProgress(task benchmark.Task, state benchmark.State) float64 {
	constraints := len(task.Oracle.RequiredFiles) + len(task.Oracle.AbsentPaths) +
		len(task.Oracle.UnchangedPaths) + 1
	if constraints <= 0 {
		return 0
	}
	failures := len(task.Evaluate(state).Failures)
	if !state.TaskComplete {
		failures++
	}
	progress := float64(constraints-failures) / float64(constraints)
	if progress < 0 {
		return 0
	}
	if progress > 1 {
		return 1
	}
	return progress
}

func predictionTransitionNLL(predictions []learning.Prediction, candidateID string) float64 {
	for _, prediction := range predictions {
		if prediction.Candidate.CandidateID != candidateID {
			continue
		}
		return -prediction.Features["transition_log_probability"]
	}
	return 0
}

func (l Loop) adaptiveConfig() (AdaptiveProposalConfig, error) {
	config := l.AdaptiveProposal
	if !config.Enabled {
		return config, nil
	}
	if config.InitialCount == 0 {
		config.InitialCount = 1
	}
	if config.MinimumValid == 0 {
		config.MinimumValid = 2
	}
	if config.MinimumSpread == 0 {
		config.MinimumSpread = 0.25
	}
	if config.InitialCount < 1 || config.InitialCount > l.Coordinator.ProposerCount {
		return AdaptiveProposalConfig{}, fmt.Errorf(
			"adaptive initial proposer count must be between 1 and %d",
			l.Coordinator.ProposerCount,
		)
	}
	if config.MinimumValid < 1 {
		return AdaptiveProposalConfig{}, fmt.Errorf("adaptive minimum valid candidates must be positive")
	}
	if config.MinimumSpread < 0 || config.MinimumSpread > 1 {
		return AdaptiveProposalConfig{}, fmt.Errorf("adaptive minimum spread must be between 0 and 1")
	}
	return config, nil
}

func expansionReasons(
	valid []action.Candidate,
	spread float64,
	config AdaptiveProposalConfig,
) []string {
	reasons := make([]string, 0, 2)
	if len(valid) < config.MinimumValid {
		reasons = append(reasons, "insufficient_valid_candidates")
	}
	if spread < config.MinimumSpread {
		reasons = append(reasons, "insufficient_objective_spread")
	}
	return reasons
}

func (l Loop) project(
	ctx context.Context,
	result *Result,
	turn int,
	candidates []action.Candidate,
	state benchmark.State,
	policy benchmark.Policy,
) ([]action.Candidate, []string, error) {
	projectContext, projectSpan := otel.Tracer("bouncer/control").Start(
		ctx,
		"constraint.project",
		trace.WithAttributes(attribute.Int("bouncer.candidate.count", len(candidates))),
	)
	defer projectSpan.End()
	projections, err := l.Projector.Evaluate(projectContext, candidates, state, policy)
	if err != nil {
		markSpanError(projectSpan, err)
		return nil, nil, fmt.Errorf("turn %d constraint projection: %w", turn, err)
	}
	if len(projections) != len(candidates) {
		return nil, nil, fmt.Errorf(
			"turn %d constraint projection returned %d results for %d candidates",
			turn,
			len(projections),
			len(candidates),
		)
	}
	valid := make([]action.Candidate, 0, len(candidates))
	feedback := make([]string, 0)
	for index, projection := range projections {
		if projection.ActionID != candidates[index].CandidateID {
			return nil, nil, fmt.Errorf(
				"turn %d constraint projection result %d action id %q does not match %q",
				turn,
				index,
				projection.ActionID,
				candidates[index].CandidateID,
			)
		}
		if err := l.record(projectContext, result, TraceEvent{
			EventType: "constraint.evaluated",
			StepID:    turn,
			Payload: map[string]any{
				"action_id":  projection.ActionID,
				"allowed":    projection.Allowed,
				"projection": projection.Projection,
			},
		}); err != nil {
			return nil, nil, err
		}
		if projection.Allowed {
			valid = append(valid, candidates[index])
			continue
		}
		result.ConstraintRejections++
		feedback = append(feedback, projection.Projection)
	}
	return valid, feedback, nil
}

func (l Loop) record(ctx context.Context, result *Result, event TraceEvent) error {
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		event.Payload["trace_id"] = spanContext.TraceID().String()
		event.Payload["span_id"] = spanContext.SpanID().String()
	}
	result.Trace = append(result.Trace, event)
	if l.TraceSink == nil {
		return nil
	}
	if err := l.TraceSink.Append(ctx, event); err != nil {
		return fmt.Errorf("append %s trace event: %w", event.EventType, err)
	}
	return nil
}

func markSpanError(span trace.Span, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

func flattenProposals(proposals []nimclient.ProposalResult, result *Result) []action.Candidate {
	capacity := 0
	for _, proposal := range proposals {
		capacity += len(proposal.Beam.Actions)
	}
	candidates := make([]action.Candidate, 0, capacity)
	for _, proposal := range proposals {
		result.PromptTokens += proposal.Usage.PromptTokens
		result.CompletionTokens += proposal.Usage.CompletionTokens
		result.ReasoningTokens += proposal.Usage.ReasoningTokens
		result.TotalTokens += proposal.Usage.TotalTokens
		for _, candidate := range proposal.Beam.Actions {
			candidate.CandidateID = namespaceCandidateID(proposal.ProposerID, candidate.CandidateID)
			candidates = append(candidates, candidate)
		}
	}
	result.GeneratedCandidates += len(candidates)
	return candidates
}

func namespaceCandidateID(proposerID, candidateID string) string {
	namespaced := proposerID + ":" + candidateID
	if len(namespaced) <= 128 {
		return namespaced
	}
	digest := sha256.Sum256([]byte(candidateID))
	return proposerID + ":sha256-" + hex.EncodeToString(digest[:16])
}
