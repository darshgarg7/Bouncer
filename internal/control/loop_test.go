package control

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"bouncer/internal/action"
	"bouncer/internal/anomaly"
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

type proposerFunc func(context.Context, nimclient.ProposalRequest) (nimclient.ProposalResult, error)

func (function proposerFunc) Propose(ctx context.Context, request nimclient.ProposalRequest) (nimclient.ProposalResult, error) {
	return function(ctx, request)
}

type projectorFunc func(context.Context, []action.Candidate, benchmark.State, benchmark.Policy) ([]projector.Result, error)

type traceSinkFunc func(context.Context, TraceEvent) error

type fixedLearningScorer struct{}

type fixedAnomalyScorer struct {
	evaluation anomaly.Evaluation
	metadata   anomaly.Metadata
	err        error
	calls      *int
}

func (s fixedAnomalyScorer) Metadata() anomaly.Metadata {
	return s.metadata
}

func (s fixedAnomalyScorer) Score(_ monitoring.Features) (anomaly.Evaluation, error) {
	if s.calls != nil {
		(*s.calls)++
	}
	return s.evaluation, s.err
}

func anomalyScorer(score float64, activeEligible bool, calls *int) fixedAnomalyScorer {
	const threshold = 0.6
	return fixedAnomalyScorer{
		evaluation: anomaly.Evaluation{
			Score:     score,
			Threshold: threshold,
			Alert:     score >= threshold,
		},
		metadata: anomaly.Metadata{
			SchemaVersion:        anomaly.ArtifactSchemaVersion,
			FeatureSchemaVersion: anomaly.FeatureSchemaVersion,
			ArtifactID:           "control-test-anomaly",
			ArtifactSHA256:       strings.Repeat("a", 64),
			Threshold:            threshold,
			ActiveEligible:       activeEligible,
		},
		calls: calls,
	}
}

func (fixedLearningScorer) Metadata() learning.Metadata {
	return learning.Metadata{
		SchemaVersion:        learning.ArtifactSchemaVersion,
		FeatureSchemaVersion: learning.FeatureSchemaVersion,
		ArtifactID:           "control-test-learning",
		ArtifactSHA256:       strings.Repeat("0", 64),
	}
}

func (fixedLearningScorer) Score(
	_ context.Context,
	_ learning.Context,
	candidates []action.ScoredCandidate,
) (learning.Batch, error) {
	predictions := make([]learning.Prediction, 0, len(candidates))
	for _, candidate := range candidates {
		progress := 0.1
		success := 0.1
		risk := 0.1
		if candidate.Candidate.OperationClass == "filesystem.write" {
			progress = 0.9
			success = 0.9
			risk = 0.01
		}
		predictions = append(predictions, learning.Prediction{
			Candidate: candidate.Candidate,
			Features: map[string]float64{
				"transition_log_probability": -1,
			},
			Progress:    learning.Estimate{Mean: progress, Conservative: progress},
			Success:     learning.Estimate{Mean: success, Conservative: success},
			LatencyMS:   learning.Estimate{Mean: 1, Conservative: 1},
			CostUnits:   learning.Estimate{Mean: 1, Conservative: 1},
			AdverseRisk: learning.Estimate{Mean: risk, Conservative: risk},
		})
	}
	return learning.Batch{
		Metadata:    fixedLearningScorer{}.Metadata(),
		Predictions: predictions,
	}, nil
}

func (function traceSinkFunc) Append(ctx context.Context, event TraceEvent) error {
	return function(ctx, event)
}

func (function projectorFunc) Evaluate(
	ctx context.Context,
	candidates []action.Candidate,
	state benchmark.State,
	policy benchmark.Policy,
) ([]projector.Result, error) {
	return function(ctx, candidates, state, policy)
}

func validCandidate(id, operation, target string, arguments map[string]any, objective float64) action.Candidate {
	return action.Candidate{
		CandidateID:          id,
		OperationClass:       operation,
		Tool:                 "virtual",
		Target:               target,
		Arguments:            arguments,
		DeclaredDependencies: []string{},
		EstimatedObjectives: action.Objectives{
			LatencyMS:  objective,
			CostUnits:  objective,
			SafetyRisk: objective / 10,
		},
	}
}

func stateAwareProposer(_ context.Context, request nimclient.ProposalRequest) (nimclient.ProposalResult, error) {
	var state benchmark.State
	if err := json.Unmarshal(request.State, &state); err != nil {
		return nimclient.ProposalResult{}, err
	}
	operation := "filesystem.write"
	target := "workspace/result.txt"
	arguments := map[string]any{"content": "ready"}
	if state.BenchmarkStep > 0 {
		operation = "task.complete"
		target = "task"
		arguments = map[string]any{}
	}
	actions := make([]action.Candidate, 0, action.BeamWidth)
	actions = append(actions, validCandidate("candidate-1", operation, target, arguments, 1))
	for index := 2; index <= action.BeamWidth; index++ {
		actions = append(actions, validCandidate(
			"candidate-"+string(rune('0'+index)),
			"filesystem.read",
			"workspace/input-"+string(rune('0'+index)),
			map[string]any{},
			float64(index),
		))
	}
	return nimclient.ProposalResult{
		ProposerID: request.ProposerID,
		Beam:       action.Beam{Actions: actions},
		Usage: nimclient.Usage{
			PromptTokens:     10,
			CompletionTokens: 15,
			ReasoningTokens:  5,
			TotalTokens:      30,
		},
		FinishReason: "stop",
	}, nil
}

func allowFirstCandidate(
	_ context.Context,
	candidates []action.Candidate,
	_ benchmark.State,
	_ benchmark.Policy,
) ([]projector.Result, error) {
	results := make([]projector.Result, len(candidates))
	for index, candidate := range candidates {
		allowed := strings.HasSuffix(candidate.CandidateID, "candidate-1")
		projection := ""
		if !allowed {
			projection = `<constraint_violation action_id="` + candidate.CandidateID + `" code="TEST_REJECTION"/>`
		}
		results[index] = projector.Result{
			ActionID:   candidate.CandidateID,
			Allowed:    allowed,
			Projection: projection,
		}
	}
	return results, nil
}

func testTask() benchmark.Task {
	return benchmark.Task{
		SchemaVersion: "0.1.0",
		TaskID:        "task-control",
		Instruction:   "write the expected file and complete",
		Policy: benchmark.Policy{
			AllowedOperationClasses: []string{"filesystem.read", "filesystem.write", "task.complete"},
			AllowedPathPrefixes:     []string{"workspace/", "task"},
			ProtectedPaths:          []string{},
			MaxMutations:            1,
		},
		InitialState: benchmark.InitialState{
			CompletedOperations: []string{},
			Files:               map[string]string{},
		},
		Oracle: benchmark.Oracle{
			RequiredFiles:  map[string]string{"workspace/result.txt": "ready"},
			AbsentPaths:    []string{},
			UnchangedPaths: []string{},
		},
	}
}

func testLoop(proposer harness.Proposer, batchProjector projector.BatchProjector) Loop {
	return Loop{
		Coordinator: harness.Coordinator{
			Proposer:      proposer,
			ProposerCount: 3,
			Timeout:       time.Second,
		},
		Projector:  batchProjector,
		Calibrator: testCalibrator(),
		Executor:   executor.Virtual{},
		MaxTurns:   3,
	}
}

func testCalibrator() calibration.Calibrator {
	runtime, err := calibration.New(calibration.Artifact{
		SchemaVersion: "0.1.0",
		CalibrationID: "control-test-identity",
		Provenance: calibration.Provenance{
			Method: "test_fixture",
		},
		InputBounds: calibration.ObjectiveBounds{
			LatencyMS:  calibration.Range{Minimum: 0, Maximum: 1000},
			CostUnits:  calibration.Range{Minimum: 0, Maximum: 1000},
			SafetyRisk: calibration.Range{Minimum: 0, Maximum: 1},
		},
		Transforms: calibration.Transforms{
			LatencyMS:  calibration.AffineTransform{Scale: 1},
			CostUnits:  calibration.AffineTransform{Scale: 1},
			SafetyRisk: calibration.PlattTransform{Slope: 1},
		},
		ModelInfluence: calibration.ModelInfluence{
			LatencyMS:  1,
			CostUnits:  1,
			SafetyRisk: 1,
		},
		OperationPriors: map[string]action.Objectives{
			"*": {LatencyMS: 1, CostUnits: 1, SafetyRisk: 0.1},
		},
	})
	if err != nil {
		panic(err)
	}
	return runtime
}

func TestLoopCompletesTaskAndAggregatesTelemetry(t *testing.T) {
	loop := testLoop(proposerFunc(stateAwareProposer), projectorFunc(allowFirstCandidate))
	result, err := loop.Run(context.Background(), testTask(), 41)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Passed || !result.TaskComplete {
		t.Fatalf("result did not pass: %+v", result)
	}
	if result.FinalState.Files["workspace/result.txt"] != "ready" {
		t.Fatalf("unexpected final state: %+v", result.FinalState)
	}
	if result.Turns != 2 || result.ModelCalls != 6 || result.GeneratedCandidates != 30 {
		t.Fatalf("unexpected execution counts: %+v", result)
	}
	if result.ConstraintRejections != 24 || result.ExecutedActions != 2 {
		t.Fatalf("unexpected routing counts: %+v", result)
	}
	if result.PromptTokens != 60 || result.CompletionTokens != 90 || result.ReasoningTokens != 30 || result.TotalTokens != 180 {
		t.Fatalf("unexpected token counts: %+v", result)
	}
	if len(result.Trace) != 36 {
		t.Fatalf("got %d trace events, want 36", len(result.Trace))
	}
	if result.RoutingStrategy != router.StrategyLexicographic || result.AdaptiveProposals {
		t.Fatalf("unexpected routing metadata: %+v", result)
	}
	if result.ObjectiveCalibration.CalibrationID != "control-test-identity" ||
		result.ObjectiveCalibration.ArtifactSHA256 == "" {
		t.Fatalf("missing objective calibration evidence: %+v", result.ObjectiveCalibration)
	}
	for _, event := range result.Trace {
		if event.EventType != "candidate.selected" {
			continue
		}
		if event.Payload["strategy"] != router.StrategyLexicographic ||
			event.Payload["selection_probability"] != 1.0 ||
			event.Payload["objective_source"] != "trusted_calibration_artifact" {
			t.Fatalf("unexpected selection evidence: %+v", event.Payload)
		}
	}
}

func TestActiveLearningReranksOnlyPolicyAdmittedCandidates(t *testing.T) {
	allowAll := projectorFunc(func(
		_ context.Context,
		candidates []action.Candidate,
		_ benchmark.State,
		_ benchmark.Policy,
	) ([]projector.Result, error) {
		results := make([]projector.Result, len(candidates))
		for index, candidate := range candidates {
			results[index] = projector.Result{ActionID: candidate.CandidateID, Allowed: true}
		}
		return results, nil
	})
	loop := testLoop(proposerFunc(stateAwareProposer), allowAll)
	loop.MaxTurns = 1
	loop.LearningScorer = fixedLearningScorer{}
	loop.Learning = LearningConfig{
		Mode: LearningActive,
		Router: router.LearnedConfig{
			RiskCeiling:            1,
			MaxRelativeUncertainty: 1,
			FrontierLimit:          16,
		},
	}
	result, err := loop.Run(context.Background(), testTask(), 41)
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalState.Files["workspace/result.txt"] != "ready" {
		t.Fatalf("learned router did not select the admitted write: %+v", result.FinalState)
	}
	if result.RoutingStrategy != "learned_pareto_safety_first" ||
		result.LearningMode != LearningActive {
		t.Fatalf("learning promotion evidence is missing: %+v", result)
	}
}

func TestShadowAnomalyRecordsAlertWithoutChangingExecution(t *testing.T) {
	calls := 0
	loop := testLoop(proposerFunc(stateAwareProposer), projectorFunc(allowFirstCandidate))
	loop.AnomalyScorer = anomalyScorer(0.8, false, &calls)
	loop.Anomaly = AnomalyConfig{Mode: AnomalyShadow}
	result, err := loop.Run(context.Background(), testTask(), 41)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.ExecutionGated || result.AnomalyGates != 0 ||
		result.AnomalyAlerts != 2 || calls != 2 {
		t.Fatalf("shadow anomaly mode changed execution or evidence: %+v calls=%d", result, calls)
	}
	if result.AnomalyArtifact == nil || result.AnomalyArtifact.ArtifactID != "control-test-anomaly" {
		t.Fatalf("missing anomaly artifact evidence: %+v", result.AnomalyArtifact)
	}
	for _, event := range result.Trace {
		if event.EventType != "execution.completed" {
			continue
		}
		monitoringEvidence, ok := event.Payload["monitoring"].(map[string]any)
		if !ok || monitoringEvidence["subsequent_execution_gated"] != false {
			t.Fatalf("shadow event incorrectly gated execution: %+v", event.Payload)
		}
	}
}

func TestActiveAnomalyStopsOnlySubsequentExecution(t *testing.T) {
	calls := 0
	loop := testLoop(proposerFunc(stateAwareProposer), projectorFunc(allowFirstCandidate))
	loop.AnomalyScorer = anomalyScorer(0.8, true, &calls)
	loop.Anomaly = AnomalyConfig{Mode: AnomalyActive}
	result, err := loop.Run(context.Background(), testTask(), 41)
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || result.TaskComplete || !result.ExecutionGated ||
		result.AnomalyGates != 1 || result.AnomalyAlerts != 1 ||
		result.ExecutedActions != 1 || result.Turns != 1 || calls != 1 {
		t.Fatalf("unexpected active anomaly result: %+v calls=%d", result, calls)
	}
	if result.FinalState.Files["workspace/result.txt"] != "ready" {
		t.Fatalf("triggering action was not recorded as executed: %+v", result.FinalState)
	}
	if !strings.Contains(strings.Join(result.OracleFailures, " "), "active anomaly gate") {
		t.Fatalf("missing anomaly gate failure: %v", result.OracleFailures)
	}
	foundGateEvidence := false
	for _, event := range result.Trace {
		if event.EventType != "execution.completed" {
			continue
		}
		monitoringEvidence, ok := event.Payload["monitoring"].(map[string]any)
		if ok && monitoringEvidence["subsequent_execution_gated"] == true {
			foundGateEvidence = true
		}
	}
	if !foundGateEvidence {
		t.Fatal("active gate was not attached to the triggering execution evidence")
	}
}

func TestActiveAnomalyRequiresEligibleArtifactBeforeProposal(t *testing.T) {
	calls := 0
	loop := testLoop(proposerFunc(stateAwareProposer), projectorFunc(allowFirstCandidate))
	loop.AnomalyScorer = anomalyScorer(0.8, false, &calls)
	loop.Anomaly = AnomalyConfig{Mode: AnomalyActive}
	_, err := loop.Run(context.Background(), testTask(), 41)
	if err == nil || !strings.Contains(err.Error(), "active-eligible") {
		t.Fatalf("got error %v", err)
	}
	if calls != 0 {
		t.Fatalf("anomaly scorer ran before configuration rejection: %d", calls)
	}
}

func TestActiveAnomalyBelowThresholdContinues(t *testing.T) {
	calls := 0
	loop := testLoop(proposerFunc(stateAwareProposer), projectorFunc(allowFirstCandidate))
	loop.AnomalyScorer = anomalyScorer(0.2, true, &calls)
	loop.Anomaly = AnomalyConfig{Mode: AnomalyActive}
	result, err := loop.Run(context.Background(), testTask(), 41)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.ExecutionGated || result.AnomalyAlerts != 0 || calls != 2 {
		t.Fatalf("below-threshold active detector changed execution: %+v calls=%d", result, calls)
	}
}

func TestAnomalyNeverScoresPolicyRejectedCandidate(t *testing.T) {
	rejectAll := projectorFunc(func(
		_ context.Context,
		candidates []action.Candidate,
		_ benchmark.State,
		_ benchmark.Policy,
	) ([]projector.Result, error) {
		results := make([]projector.Result, len(candidates))
		for index, candidate := range candidates {
			results[index] = projector.Result{
				ActionID:   candidate.CandidateID,
				Allowed:    false,
				Projection: `<constraint_violation action_id="` + candidate.CandidateID + `" code="DENIED"/>`,
			}
		}
		return results, nil
	})
	calls := 0
	loop := testLoop(proposerFunc(stateAwareProposer), rejectAll)
	loop.MaxTurns = 1
	loop.AnomalyScorer = anomalyScorer(0.8, false, &calls)
	loop.Anomaly = AnomalyConfig{Mode: AnomalyShadow}
	result, err := loop.Run(context.Background(), testTask(), 41)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExecutedActions != 0 || calls != 0 || result.AnomalyAlerts != 0 {
		t.Fatalf("rejected candidate reached anomaly or execution: %+v calls=%d", result, calls)
	}
}

func TestShadowAnomalyScoringFailureIsRecordedWithoutChangingControlFlow(t *testing.T) {
	planned := errors.New("planned anomaly scorer failure")
	loop := testLoop(proposerFunc(stateAwareProposer), projectorFunc(allowFirstCandidate))
	scorer := anomalyScorer(0, false, nil)
	scorer.err = planned
	loop.AnomalyScorer = scorer
	loop.Anomaly = AnomalyConfig{Mode: AnomalyShadow}
	recorded := make([]TraceEvent, 0)
	loop.TraceSink = traceSinkFunc(func(_ context.Context, event TraceEvent) error {
		recorded = append(recorded, event)
		return nil
	})
	result, err := loop.Run(context.Background(), testTask(), 41)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.AnomalyScoringErrors != 2 || result.ExecutionGated {
		t.Fatalf("shadow scoring failure changed control flow: %+v", result)
	}
	if len(recorded) == 0 || recorded[len(recorded)-1].EventType != "execution.completed" {
		t.Fatalf("completed side effect was not recorded with scorer failure: %+v", recorded)
	}
	monitoringEvidence := recorded[len(recorded)-1].Payload["monitoring"].(map[string]any)
	anomalyEvidence := monitoringEvidence["anomaly"].(map[string]any)
	if anomalyEvidence["scoring_error"] != planned.Error() {
		t.Fatalf("scoring failure evidence is missing: %+v", anomalyEvidence)
	}
}

func TestActiveAnomalyScoringFailureRecordsExecutionThenFailsClosed(t *testing.T) {
	planned := errors.New("planned active anomaly scorer failure")
	loop := testLoop(proposerFunc(stateAwareProposer), projectorFunc(allowFirstCandidate))
	scorer := anomalyScorer(0, true, nil)
	scorer.err = planned
	loop.AnomalyScorer = scorer
	loop.Anomaly = AnomalyConfig{Mode: AnomalyActive}
	recorded := make([]TraceEvent, 0)
	loop.TraceSink = traceSinkFunc(func(_ context.Context, event TraceEvent) error {
		recorded = append(recorded, event)
		return nil
	})
	_, err := loop.Run(context.Background(), testTask(), 41)
	if !errors.Is(err, planned) {
		t.Fatalf("got error %v", err)
	}
	executions := 0
	for _, event := range recorded {
		if event.EventType != "execution.completed" {
			continue
		}
		executions++
		monitoringEvidence := event.Payload["monitoring"].(map[string]any)
		anomalyEvidence := monitoringEvidence["anomaly"].(map[string]any)
		if anomalyEvidence["scoring_error"] != planned.Error() {
			t.Fatalf("active scoring failure evidence is missing: %+v", anomalyEvidence)
		}
	}
	if executions != 1 {
		t.Fatalf("active scoring failure allowed subsequent execution: %+v", recorded)
	}
}

func TestLoopExpandsAdaptiveProposalBudget(t *testing.T) {
	project := projectorFunc(func(
		_ context.Context,
		candidates []action.Candidate,
		_ benchmark.State,
		_ benchmark.Policy,
	) ([]projector.Result, error) {
		results := make([]projector.Result, len(candidates))
		for index, candidate := range candidates {
			allowed := strings.HasPrefix(candidate.CandidateID, "agent-2:") &&
				strings.HasSuffix(candidate.CandidateID, "candidate-1")
			projection := ""
			if !allowed {
				projection = `<constraint_violation action_id="` + candidate.CandidateID + `" code="TEST_REJECTION"/>`
			}
			results[index] = projector.Result{
				ActionID:   candidate.CandidateID,
				Allowed:    allowed,
				Projection: projection,
			}
		}
		return results, nil
	})
	loop := testLoop(proposerFunc(stateAwareProposer), project)
	loop.MaxTurns = 1
	loop.AdaptiveProposal = AdaptiveProposalConfig{
		Enabled:       true,
		InitialCount:  1,
		MinimumValid:  1,
		MinimumSpread: 0.1,
	}
	result, err := loop.Run(context.Background(), testTask(), 1)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.ModelCalls != 3 || result.ProposalExpansions != 1 || result.ExecutedActions != 1 {
		t.Fatalf("unexpected adaptive execution: %+v", result)
	}
	foundExpansion := false
	for _, event := range result.Trace {
		if event.EventType == "proposal.expanded" {
			foundExpansion = true
		}
	}
	if !foundExpansion {
		t.Fatal("trace does not contain proposal.expanded")
	}
}

func TestLoopFailsClosedWhenEveryCandidateIsRejected(t *testing.T) {
	rejectAll := projectorFunc(func(
		_ context.Context,
		candidates []action.Candidate,
		_ benchmark.State,
		_ benchmark.Policy,
	) ([]projector.Result, error) {
		results := make([]projector.Result, len(candidates))
		for index, candidate := range candidates {
			results[index] = projector.Result{
				ActionID:   candidate.CandidateID,
				Allowed:    false,
				Projection: `<constraint_violation action_id="` + candidate.CandidateID + `" code="DENIED"/>`,
			}
		}
		return results, nil
	})
	loop := testLoop(proposerFunc(stateAwareProposer), rejectAll)
	loop.MaxTurns = 1
	result, err := loop.Run(context.Background(), testTask(), 1)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Passed || result.ExecutedActions != 0 || result.ConstraintRejections != 15 {
		t.Fatalf("unexpected fail-closed result: %+v", result)
	}
	if len(result.FinalState.ConstraintFeedback) != 15 {
		t.Fatalf("got %d feedback entries", len(result.FinalState.ConstraintFeedback))
	}
	if !strings.Contains(strings.Join(result.OracleFailures, " "), "required file") {
		t.Fatalf("missing oracle failure: %v", result.OracleFailures)
	}
}

func TestLoopRejectsInvalidConfiguration(t *testing.T) {
	_, err := (Loop{MaxTurns: 1}).Run(context.Background(), testTask(), 1)
	if err == nil || !strings.Contains(err.Error(), "projector is required") {
		t.Fatalf("got error %v", err)
	}
	_, err = (Loop{Projector: projectorFunc(allowFirstCandidate)}).Run(context.Background(), testTask(), 1)
	if err == nil || !strings.Contains(err.Error(), "max turns must be positive") {
		t.Fatalf("got error %v", err)
	}
	_, err = (Loop{
		Projector: projectorFunc(allowFirstCandidate),
		MaxTurns:  1,
	}).Run(context.Background(), testTask(), 1)
	if err == nil || !strings.Contains(err.Error(), "executor is required") {
		t.Fatalf("got error %v", err)
	}
	_, err = (Loop{
		Projector: projectorFunc(allowFirstCandidate),
		Executor:  executor.Virtual{},
		MaxTurns:  1,
	}).Run(context.Background(), testTask(), 1)
	if err == nil || !strings.Contains(err.Error(), "objective calibrator is required") {
		t.Fatalf("got error %v", err)
	}
}

func TestLoopFailsClosedWhenDurableTraceSinkFails(t *testing.T) {
	planned := errors.New("event store unavailable")
	loop := testLoop(proposerFunc(stateAwareProposer), projectorFunc(allowFirstCandidate))
	loop.TraceSink = traceSinkFunc(func(context.Context, TraceEvent) error { return planned })
	_, err := loop.Run(context.Background(), testTask(), 1)
	if !errors.Is(err, planned) || !strings.Contains(err.Error(), "proposal.completed") {
		t.Fatalf("got error %v", err)
	}
}

func TestLoopPropagatesStageFailures(t *testing.T) {
	planned := errors.New("planned failure")
	tests := []struct {
		name       string
		proposer   harness.Proposer
		projector  projector.BatchProjector
		wantPhrase string
	}{
		{
			name: "proposal",
			proposer: proposerFunc(func(context.Context, nimclient.ProposalRequest) (nimclient.ProposalResult, error) {
				return nimclient.ProposalResult{}, planned
			}),
			projector:  projectorFunc(allowFirstCandidate),
			wantPhrase: "proposal round",
		},
		{
			name:     "projection",
			proposer: proposerFunc(stateAwareProposer),
			projector: projectorFunc(func(context.Context, []action.Candidate, benchmark.State, benchmark.Policy) ([]projector.Result, error) {
				return nil, planned
			}),
			wantPhrase: "constraint projection",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := testLoop(test.proposer, test.projector).Run(context.Background(), testTask(), 1)
			if !errors.Is(err, planned) || !strings.Contains(err.Error(), test.wantPhrase) {
				t.Fatalf("got error %v", err)
			}
		})
	}
}

func TestLoopValidatesProjectionCardinalityAndOrder(t *testing.T) {
	tests := []struct {
		name       string
		projector  projector.BatchProjector
		wantPhrase string
	}{
		{
			name: "cardinality",
			projector: projectorFunc(func(context.Context, []action.Candidate, benchmark.State, benchmark.Policy) ([]projector.Result, error) {
				return []projector.Result{}, nil
			}),
			wantPhrase: "0 results for 15 candidates",
		},
		{
			name: "order",
			projector: projectorFunc(func(_ context.Context, candidates []action.Candidate, _ benchmark.State, _ benchmark.Policy) ([]projector.Result, error) {
				results, err := allowFirstCandidate(context.Background(), candidates, benchmark.State{}, benchmark.Policy{})
				results[0].ActionID = "wrong-id"
				return results, err
			}),
			wantPhrase: "does not match",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := testLoop(proposerFunc(stateAwareProposer), test.projector).Run(context.Background(), testTask(), 1)
			if err == nil || !strings.Contains(err.Error(), test.wantPhrase) {
				t.Fatalf("got error %v", err)
			}
		})
	}
}

func TestLoopPropagatesRouterAndExecutorFailures(t *testing.T) {
	malformedProposer := proposerFunc(func(_ context.Context, request nimclient.ProposalRequest) (nimclient.ProposalResult, error) {
		result, err := stateAwareProposer(context.Background(), request)
		result.Beam.Actions[0].OperationClass = "not.real"
		return result, err
	})
	_, err := testLoop(malformedProposer, projectorFunc(allowFirstCandidate)).Run(context.Background(), testTask(), 1)
	if err == nil || !strings.Contains(err.Error(), "route candidates") {
		t.Fatalf("got router error %v", err)
	}

	missingContentProposer := proposerFunc(func(_ context.Context, request nimclient.ProposalRequest) (nimclient.ProposalResult, error) {
		result, err := stateAwareProposer(context.Background(), request)
		result.Beam.Actions[0].Arguments = map[string]any{}
		return result, err
	})
	_, err = testLoop(missingContentProposer, projectorFunc(allowFirstCandidate)).Run(context.Background(), testTask(), 1)
	if err == nil || !strings.Contains(err.Error(), "execute") {
		t.Fatalf("got executor error %v", err)
	}
}

func TestNamespaceCandidateIDPreservesShortID(t *testing.T) {
	if got := namespaceCandidateID("agent-1", "candidate-1"); got != "agent-1:candidate-1" {
		t.Fatalf("got %q", got)
	}
}

func TestNamespaceCandidateIDBoundsLongIDDeterministically(t *testing.T) {
	original := strings.Repeat("a", 128)
	first := namespaceCandidateID("agent-1", original)
	second := namespaceCandidateID("agent-1", original)
	if first != second {
		t.Fatalf("namespacing is not deterministic: %q != %q", first, second)
	}
	if len(first) > 128 {
		t.Fatalf("namespaced id has %d bytes", len(first))
	}
	if !strings.HasPrefix(first, "agent-1:sha256-") {
		t.Fatalf("unexpected long id encoding: %q", first)
	}
}
