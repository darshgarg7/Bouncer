package policy_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
	"bouncer/internal/policy"
	"bouncer/internal/projector"
)

func TestEvaluatorMatchesPythonReferenceAcrossGeneratedCases(t *testing.T) {
	goEvaluator, err := policy.Load("../../configs/skill_dag.json")
	if err != nil {
		t.Fatal(err)
	}
	caseCount := 10_000
	if configured := os.Getenv("BOUNCER_POLICY_PARITY_CASES"); configured != "" {
		parsed, err := strconv.Atoi(configured)
		if err != nil || parsed < 1 {
			t.Fatalf("invalid BOUNCER_POLICY_PARITY_CASES %q", configured)
		}
		caseCount = parsed
	}
	candidates := generatedCandidates(caseCount)
	state := benchmark.State{
		CompletedOperations: []string{"filesystem.read"},
		Files:               map[string]string{"workspace/protected/secret.txt": "secret"},
		MutationCount:       1,
	}
	taskPolicy := benchmark.Policy{
		AllowedOperationClasses: []string{
			"filesystem.read",
			"filesystem.write",
			"state.validate",
			"task.complete",
		},
		AllowedPathPrefixes: []string{"workspace", "task"},
		ProtectedPaths:      []string{"workspace/protected"},
		MaxMutations:        1,
	}
	goResults, err := goEvaluator.Evaluate(context.Background(), candidates, state, taskPolicy)
	if err != nil {
		t.Fatal(err)
	}
	pythonResults, err := (projector.PythonClient{WorkingDir: "../.."}).Evaluate(
		context.Background(),
		candidates,
		state,
		taskPolicy,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(goResults) != len(pythonResults) {
		t.Fatalf("Go returned %d results and Python returned %d", len(goResults), len(pythonResults))
	}
	for index := range goResults {
		if !reflect.DeepEqual(goResults[index], pythonResults[index]) {
			t.Fatalf(
				"case %d differs\nGo:     %+v\nPython: %+v",
				index,
				goResults[index],
				pythonResults[index],
			)
		}
	}
}

func TestEvaluatorRejectsMalformedDAGAndInputs(t *testing.T) {
	tests := []struct {
		name       string
		operations map[string][]string
		want       string
	}{
		{name: "nil", operations: nil, want: "must be an object"},
		{name: "empty operation", operations: map[string][]string{"": {}}, want: "non-empty"},
		{name: "duplicate", operations: map[string][]string{"read": {}, "write": {"read", "read"}}, want: "duplicates"},
		{name: "unknown dependency", operations: map[string][]string{"write": {"read"}}, want: "unknown dependency"},
		{name: "cycle", operations: map[string][]string{"left": {"right"}, "right": {"left"}}, want: "cycle"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := policy.New(test.operations)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got error %v", err)
			}
		})
	}

	evaluator, err := policy.New(map[string][]string{"filesystem.read": {}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = evaluator.Evaluate(
		context.Background(),
		[]action.Candidate{candidate("one", "filesystem.read", "workspace/file")},
		benchmark.State{CompletedOperations: nil},
		benchmark.Policy{
			AllowedOperationClasses: []string{"filesystem.read"},
			AllowedPathPrefixes:     []string{"workspace"},
			ProtectedPaths:          []string{},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "completed_operations") {
		t.Fatalf("got error %v", err)
	}
}

func TestEvaluatorFailsClosedOnCancellationAndEmptyBatch(t *testing.T) {
	evaluator, err := policy.New(map[string][]string{"filesystem.read": {}})
	if err != nil {
		t.Fatal(err)
	}
	state := benchmark.State{CompletedOperations: []string{}, Files: map[string]string{}}
	taskPolicy := benchmark.Policy{
		AllowedOperationClasses: []string{"filesystem.read"},
		AllowedPathPrefixes:     []string{"workspace"},
		ProtectedPaths:          []string{},
	}
	if _, err := evaluator.Evaluate(context.Background(), nil, state, taskPolicy); err == nil {
		t.Fatal("empty batch was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := evaluator.Evaluate(
		ctx,
		[]action.Candidate{candidate("one", "filesystem.read", "workspace/file")},
		state,
		taskPolicy,
	); err == nil {
		t.Fatal("canceled evaluation succeeded")
	}
}

func generatedCandidates(count int) []action.Candidate {
	operations := []string{
		"filesystem.read",
		"filesystem.write",
		"state.validate",
		"task.complete",
		"service.deploy",
		"not.real",
	}
	targets := []string{
		"workspace/service/config.yaml",
		"workspace/protected/secret.txt",
		"outside/file.txt",
		"workspace/../escape.txt",
		"workspace/service/a\"b.txt",
		"task",
	}
	result := make([]action.Candidate, count)
	for index := range count {
		result[index] = candidate(
			fmt.Sprintf("case-%05d", index),
			operations[index%len(operations)],
			targets[(index/len(operations))%len(targets)],
		)
	}
	return result
}

func candidate(id, operation, target string) action.Candidate {
	return action.Candidate{
		CandidateID:          id,
		OperationClass:       operation,
		Tool:                 "test_tool",
		Target:               target,
		Arguments:            map[string]any{},
		DeclaredDependencies: []string{},
		EstimatedObjectives: action.Objectives{
			LatencyMS:  1,
			CostUnits:  0.1,
			SafetyRisk: 0.2,
		},
	}
}
