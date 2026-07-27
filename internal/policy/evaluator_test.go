package policy_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
		DeniedReadPaths:     []string{"workspace/protected/secret.txt"},
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

func TestEvaluatorEnforcesExplicitReadDenyRules(t *testing.T) {
	evaluator, err := policy.New(map[string][]string{"filesystem.read": {}})
	if err != nil {
		t.Fatal(err)
	}
	results, err := evaluator.Evaluate(
		context.Background(),
		[]action.Candidate{candidate("read-secret", "filesystem.read", "workspace/secrets/token")},
		benchmark.State{CompletedOperations: []string{}, Files: map[string]string{}},
		benchmark.Policy{
			AllowedOperationClasses: []string{"filesystem.read"},
			AllowedPathPrefixes:     []string{"workspace"},
			ProtectedPaths:          []string{},
			DeniedReadPaths:         []string{"workspace/secrets"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Allowed || len(results[0].Violations) != 1 || results[0].Violations[0].Code != "READ_DENIED" {
		t.Fatalf("read deny rule was not enforced: %+v", results[0])
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

func TestEvaluatorRejectsEveryMalformedCandidateField(t *testing.T) {
	evaluator, err := policy.New(map[string][]string{"filesystem.read": {}})
	if err != nil {
		t.Fatal(err)
	}
	base := candidate("candidate", "filesystem.read", "workspace/file")
	tests := map[string]func(*action.Candidate){
		"candidate ID":     func(value *action.Candidate) { value.CandidateID = " bad" },
		"operation":        func(value *action.Candidate) { value.OperationClass = " " },
		"tool":             func(value *action.Candidate) { value.Tool = " " },
		"target":           func(value *action.Candidate) { value.Target = " " },
		"arguments":        func(value *action.Candidate) { value.Arguments = nil },
		"nil deps":         func(value *action.Candidate) { value.DeclaredDependencies = nil },
		"empty dep":        func(value *action.Candidate) { value.DeclaredDependencies = []string{""} },
		"long dep":         func(value *action.Candidate) { value.DeclaredDependencies = []string{strings.Repeat("x", 129)} },
		"duplicate dep":    func(value *action.Candidate) { value.DeclaredDependencies = []string{"read", "read"} },
		"negative latency": func(value *action.Candidate) { value.EstimatedObjectives.LatencyMS = -1 },
		"negative cost":    func(value *action.Candidate) { value.EstimatedObjectives.CostUnits = -1 },
		"large risk":       func(value *action.Candidate) { value.EstimatedObjectives.SafetyRisk = 2 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := base
			mutate(&value)
			results, err := evaluator.Evaluate(
				context.Background(),
				[]action.Candidate{value},
				benchmark.State{CompletedOperations: []string{}},
				benchmark.Policy{
					AllowedOperationClasses: []string{"filesystem.read"},
					AllowedPathPrefixes:     []string{"workspace"},
					ProtectedPaths:          []string{},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if results[0].Allowed || results[0].Violations[0].Code != "INVALID_ACTION" {
				t.Fatalf("malformed candidate was not rejected: %+v", results[0])
			}
		})
	}
}

func TestPolicyLoadAndCanonicalEscapingFailurePaths(t *testing.T) {
	if _, err := policy.Load(filepath.Join(t.TempDir(), "missing")); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("missing policy returned %v", err)
	}
	for name, content := range map[string]string{
		"unknown":  `{"schema_version":"0.1.0","operations":{},"unknown":true}`,
		"trailing": `{"schema_version":"0.1.0","operations":{}} {}`,
		"version":  `{"schema_version":"old","operations":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dag.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := policy.Load(path); err == nil {
				t.Fatal("Load accepted malformed policy")
			}
		})
	}
	evaluator, err := policy.New(map[string][]string{"filesystem.read": {}})
	if err != nil {
		t.Fatal(err)
	}
	results, err := evaluator.Evaluate(
		context.Background(),
		[]action.Candidate{candidate("escape", "filesystem.read", `other/a'b"`)},
		benchmark.State{CompletedOperations: []string{}},
		benchmark.Policy{
			AllowedOperationClasses: []string{"filesystem.read"},
			AllowedPathPrefixes:     []string{"workspace"},
			ProtectedPaths:          []string{},
		},
	)
	if err != nil || !strings.Contains(results[0].Projection, "&quot;") {
		t.Fatalf("projection did not escape canonical attribute: %+v error=%v", results, err)
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
