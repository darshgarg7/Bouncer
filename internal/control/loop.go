package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
	"bouncer/internal/executor"
	"bouncer/internal/harness"
	"bouncer/internal/nimclient"
	"bouncer/internal/projector"
	"bouncer/internal/router"
)

type Loop struct {
	Coordinator      harness.Coordinator
	Projector        projector.BatchProjector
	Executor         executor.Executor
	TraceSink        TraceSink
	RouterConfig     router.Config
	AdaptiveProposal AdaptiveProposalConfig
	MaxTurns         int
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
	Condition            string          `json:"condition"`
	TaskID               string          `json:"task_id"`
	Seed                 int64           `json:"seed"`
	Passed               bool            `json:"passed"`
	TaskComplete         bool            `json:"task_complete"`
	OracleFailures       []string        `json:"oracle_failures"`
	Turns                int             `json:"turns"`
	ModelCalls           int             `json:"model_calls"`
	PromptTokens         int             `json:"prompt_tokens"`
	CompletionTokens     int             `json:"completion_tokens"`
	ReasoningTokens      int             `json:"reasoning_tokens"`
	TotalTokens          int             `json:"total_tokens"`
	GeneratedCandidates  int             `json:"generated_candidates"`
	ConstraintRejections int             `json:"constraint_rejections"`
	ExecutedActions      int             `json:"executed_actions"`
	SevereMutations      int             `json:"severe_mutations"`
	RoutingStrategy      string          `json:"routing_strategy"`
	AdaptiveProposals    bool            `json:"adaptive_proposals"`
	ProposalExpansions   int             `json:"proposal_expansions"`
	DurationMS           int64           `json:"duration_ms"`
	FinalState           benchmark.State `json:"final_state"`
	Trace                []TraceEvent    `json:"trace"`
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
	routerConfig := l.RouterConfig
	if routerConfig == (router.Config{}) {
		routerConfig = router.DefaultConfig()
	}
	if err := routerConfig.Validate(); err != nil {
		return Result{}, fmt.Errorf("control loop router configuration: %w", err)
	}
	adaptive, err := l.adaptiveConfig()
	if err != nil {
		return Result{}, err
	}
	started := time.Now()
	state := task.NewState()
	result := Result{
		Condition:         "bouncer",
		TaskID:            task.TaskID,
		Seed:              seed,
		OracleFailures:    []string{},
		RoutingStrategy:   routerConfig.Strategy,
		AdaptiveProposals: adaptive.Enabled,
		FinalState:        state,
		Trace:             []TraceEvent{},
	}

	for turn := 0; turn < l.MaxTurns && !state.TaskComplete; turn++ {
		stateJSON, err := state.JSON()
		if err != nil {
			return Result{}, err
		}
		request := harness.Request{
			TaskID:      task.TaskID,
			Instruction: task.Instruction,
			State:       stateJSON,
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
		if adaptive.Enabled && initialCount < l.Coordinator.ProposerCount {
			spread := router.DiversitySpread(valid)
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
			}
		}
		if len(valid) == 0 {
			sort.Strings(feedback)
			state.ConstraintFeedback = feedback
			continue
		}

		turnRouterConfig := routerConfig
		turnRouterConfig.RandomSeed = seed + int64(turn)
		routingContext, routingSpan := otel.Tracer("bouncer/control").Start(
			ctx,
			"candidate.route",
			trace.WithAttributes(attribute.String("bouncer.routing.strategy", turnRouterConfig.Strategy)),
		)
		selection, err := router.SelectWithConfig(valid, turnRouterConfig)
		if err != nil {
			markSpanError(routingSpan, err)
			routingSpan.End()
			return Result{}, fmt.Errorf("turn %d route candidates: %w", turn, err)
		}
		if err := l.record(routingContext, &result, TraceEvent{
			EventType: "candidate.selected",
			StepID:    turn,
			Payload: map[string]any{
				"action_id":             selection.Selected.CandidateID,
				"strategy":              selection.Strategy,
				"selection_probability": selection.SelectionProbability,
				"selection_score":       selection.SelectionScore,
				"risk_ceiling":          turnRouterConfig.RiskCeiling,
				"epsilon":               turnRouterConfig.Epsilon,
				"weights":               turnRouterConfig.Weights,
				"ranked":                selection.Ranked,
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
		outcome, err := l.Executor.Execute(executionContext, &state, task.Policy, selection.Selected)
		if err != nil {
			markSpanError(executionSpan, err)
			executionSpan.End()
			return Result{}, fmt.Errorf("turn %d execute %s: %w", turn, selection.Selected.CandidateID, err)
		}
		result.ExecutedActions++
		if err := l.record(executionContext, &result, TraceEvent{
			EventType: "execution.completed",
			StepID:    turn,
			Payload: map[string]any{
				"action_id": selection.Selected.CandidateID,
				"operation": selection.Selected.OperationClass,
				"outcome":   outcome,
			},
		}); err != nil {
			markSpanError(executionSpan, err)
			executionSpan.End()
			return Result{}, err
		}
		executionSpan.End()
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
