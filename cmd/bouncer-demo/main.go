package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
	"bouncer/internal/eventlog"
	"bouncer/internal/executor"
	"bouncer/internal/learning"
	"bouncer/internal/policy"
	"bouncer/internal/router"
)

const (
	demoRunID  = "demo-run"
	demoTaskID = "demo-task"
)

func main() {
	projectRoot := flag.String("project-root", ".", "repository root containing configs")
	flag.Parse()
	if err := runDemo(*projectRoot, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "demo failed: %v\n", err)
		os.Exit(1)
	}
}

func runDemo(root string, output io.Writer) error {
	fmt.Fprintln(output, "Bouncer live demo — no credentials, network, or Docker")

	malformed := []byte(`{"actions":[{"candidate_id":"bad","unexpected":true}]}`)
	_, malformedErr := action.DecodeBeamStrictWidth(malformed, 1)
	if malformedErr == nil {
		return errors.New("malformed proposal was accepted")
	}
	fmt.Fprintf(output, "[PASS] 1/5 malformed proposal rejected: %v\n", malformedErr)

	evaluator, err := policy.Load(filepath.Join(root, "configs", "skill_dag.json"))
	if err != nil {
		return err
	}
	state := benchmark.State{
		CompletedOperations: []string{"filesystem.read"},
		Files: map[string]string{
			"workspace/demo/config.yaml": "enabled: false\n",
			"workspace/demo/secrets.env": "TOKEN=never-read\n",
		},
	}
	policyConfig := benchmark.Policy{
		AllowedOperationClasses: []string{"filesystem.read", "filesystem.write"},
		AllowedPathPrefixes:     []string{"workspace/demo/"},
		ProtectedPaths:          []string{"workspace/demo/secrets.env"},
		DeniedReadPaths:         []string{"workspace/demo/secrets.env"},
		MaxMutations:            1,
	}
	dangerous := candidate(
		"dangerous-read",
		"filesystem.read",
		"workspace/demo/secrets.env",
		map[string]any{},
	)
	dangerousResults, err := evaluator.Evaluate(
		context.Background(),
		[]action.Candidate{dangerous},
		state,
		policyConfig,
	)
	if err != nil {
		return err
	}
	if dangerousResults[0].Allowed {
		return errors.New("dangerous action passed policy")
	}
	codes := violationCodes(dangerousResults[0])
	fmt.Fprintf(
		output,
		"[PASS] 2/5 policy rejected dangerous action: %s\n",
		strings.Join(codes, ","),
	)
	fmt.Fprintln(output, "         audit: action=dangerous-read allowed=false executed=false")

	safe := candidate(
		"safe-write",
		"filesystem.write",
		"workspace/demo/config.yaml",
		map[string]any{"content": "enabled: true\n"},
	)
	safeResults, err := evaluator.Evaluate(
		context.Background(),
		[]action.Candidate{safe},
		state,
		policyConfig,
	)
	if err != nil {
		return err
	}
	if !safeResults[0].Allowed {
		return fmt.Errorf("safe action failed policy: %v", safeResults[0].Violations)
	}

	learningRuntime, err := learning.Load(
		filepath.Join(root, "configs", "learning-artifact.bootstrap.json"),
	)
	if err != nil {
		return err
	}
	readAlternative := candidate(
		"safe-read",
		"filesystem.read",
		"workspace/demo/config.yaml",
		map[string]any{},
	)
	scored := []action.ScoredCandidate{
		{Candidate: safe, RoutingObjectives: safe.EstimatedObjectives},
		{Candidate: readAlternative, RoutingObjectives: readAlternative.EstimatedObjectives},
	}
	learnedBatch, err := learningRuntime.Score(context.Background(), learning.Context{
		TaskID:   demoTaskID,
		Turn:     0,
		MaxTurns: 4,
		State:    state,
		Policy:   policyConfig,
	}, scored)
	if err != nil {
		return err
	}
	shadow, err := router.SelectLearned(learnedBatch.Predictions, router.LearnedConfig{
		RiskCeiling:            1,
		MaxRelativeUncertainty: 10,
		FrontierLimit:          16,
	})
	if err != nil {
		return err
	}

	outcome, err := (executor.Virtual{}).Execute(
		context.Background(),
		&state,
		policyConfig,
		safe,
	)
	if err != nil {
		return err
	}
	if state.Files[safe.Target] != "enabled: true\n" || !outcome.Mutation {
		return errors.New("safe action did not produce the expected state transition")
	}
	fmt.Fprintf(
		output,
		"[PASS] 3/5 safe action executed: modified=%s content=%q\n",
		strings.Join(outcome.Diff.Modified, ","),
		strings.TrimSpace(state.Files[safe.Target]),
	)

	logData, verification, tamperErr, err := buildAndVerifyEvidence(
		malformedErr,
		dangerous,
		dangerousResults[0],
		safe,
		outcome,
		learningRuntime.Metadata(),
		shadow,
	)
	if err != nil {
		return err
	}
	if tamperErr == nil {
		return errors.New("tampered event chain verified successfully")
	}
	fmt.Fprintf(output, "[PASS] 4/5 tamper detected: %v\n", tamperErr)
	fmt.Fprintf(
		output,
		"         verified audit: events=%d terminal=%s final_hash=%s... bytes=%d\n",
		verification.Events,
		verification.TerminalEvent,
		verification.FinalHash[:12],
		len(logData),
	)
	fmt.Fprintf(
		output,
		"[PASS] 5/5 learned routing shadowed: baseline=%s shadow=%s executed=%s\n",
		safe.CandidateID,
		shadow.Selected.Candidate.CandidateID,
		safe.CandidateID,
	)
	fmt.Fprintf(
		output,
		"         artifact=%s mode=shadow authority=deterministic-policy\n",
		learningRuntime.Metadata().ArtifactID,
	)
	fmt.Fprintln(output, "DEMO PASSED: 5/5 control boundaries behaved as expected")
	return nil
}

func candidate(id, operation, target string, arguments map[string]any) action.Candidate {
	return action.Candidate{
		CandidateID:          id,
		OperationClass:       operation,
		Tool:                 strings.ReplaceAll(operation, ".", "_"),
		Target:               target,
		Arguments:            arguments,
		DeclaredDependencies: []string{"filesystem.read"},
		EstimatedObjectives: action.Objectives{
			LatencyMS:  25,
			CostUnits:  0.01,
			SafetyRisk: 0.05,
		},
	}
}

func violationCodes(result policy.Result) []string {
	codes := make([]string, len(result.Violations))
	for index, violation := range result.Violations {
		codes[index] = violation.Code
	}
	return codes
}

func buildAndVerifyEvidence(
	malformedErr error,
	dangerous action.Candidate,
	dangerousResult policy.Result,
	safe action.Candidate,
	outcome executor.Outcome,
	metadata learning.Metadata,
	shadow router.LearnedSelection,
) ([]byte, eventlog.Verification, error, error) {
	var log bytes.Buffer
	writer := eventlog.NewWriter(&log)
	events := []eventlog.Event{
		{
			EventType: "run.started",
			RunID:     demoRunID,
			TaskID:    demoTaskID,
			Payload:   map[string]any{"mode": "local-demo"},
		},
		{
			EventType: "proposal.failed",
			RunID:     demoRunID,
			TaskID:    demoTaskID,
			Payload:   map[string]any{"error": malformedErr.Error(), "executed": false},
		},
		{
			EventType: "constraint.evaluated",
			RunID:     demoRunID,
			TaskID:    demoTaskID,
			Payload: map[string]any{
				"candidate": dangerous,
				"decision":  dangerousResult,
				"executed":  false,
			},
		},
		{
			EventType: "candidate.selected",
			RunID:     demoRunID,
			TaskID:    demoTaskID,
			Payload: map[string]any{
				"action_id": safe.CandidateID,
				"learning": map[string]any{
					"mode":             "shadow",
					"artifact":         metadata,
					"shadow_action_id": shadow.Selected.Candidate.CandidateID,
				},
			},
		},
		{
			EventType: "execution.completed",
			RunID:     demoRunID,
			TaskID:    demoTaskID,
			Payload:   map[string]any{"action_id": safe.CandidateID, "outcome": outcome},
		},
		{
			EventType: "run.completed",
			RunID:     demoRunID,
			TaskID:    demoTaskID,
			Payload:   map[string]any{"passed": true},
		},
	}
	for _, event := range events {
		if err := writer.Append(event); err != nil {
			return nil, eventlog.Verification{}, nil, err
		}
	}
	data := log.Bytes()
	verification, err := eventlog.Verify(bytes.NewReader(data))
	if err != nil {
		return nil, eventlog.Verification{}, nil, err
	}
	tampered := bytes.Replace(
		data,
		[]byte("workspace/demo/secrets.env"),
		[]byte("workspace/demo/secretz.env"),
		1,
	)
	if bytes.Equal(data, tampered) {
		return nil, eventlog.Verification{}, nil, errors.New("demo evidence target was absent")
	}
	_, tamperErr := eventlog.Verify(bytes.NewReader(tampered))
	return append([]byte(nil), data...), verification, tamperErr, nil
}
