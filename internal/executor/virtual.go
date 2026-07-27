package executor

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
)

type StateDiff struct {
	Created            []string `json:"created"`
	Modified           []string `json:"modified"`
	Deleted            []string `json:"deleted"`
	CompletedOperation string   `json:"completed_operation"`
}

type Outcome struct {
	Diff     StateDiff `json:"state_diff"`
	Mutation bool      `json:"mutation"`
	Complete bool      `json:"complete"`
}

type Virtual struct{}

type Executor interface {
	Execute(context.Context, *benchmark.State, benchmark.Policy, action.Candidate) (Outcome, error)
}

func (Virtual) Execute(_ context.Context, state *benchmark.State, policy benchmark.Policy, candidate action.Candidate) (Outcome, error) {
	if state == nil {
		return Outcome{}, errors.New("executor state is required")
	}
	diff := StateDiff{Created: []string{}, Modified: []string{}, Deleted: []string{}}
	mutating := candidate.OperationClass == "filesystem.write" || candidate.OperationClass == "filesystem.delete" || candidate.OperationClass == "service.deploy"
	if mutating && state.MutationCount >= policy.MaxMutations {
		return Outcome{}, fmt.Errorf("mutation limit %d exhausted", policy.MaxMutations)
	}

	switch candidate.OperationClass {
	case "filesystem.read", "state.validate", "state.backup", "command.run":
		addCompletedOperation(state, candidate.OperationClass)
		diff.CompletedOperation = candidate.OperationClass
	case "filesystem.write":
		content, ok := candidate.Arguments["content"].(string)
		if !ok {
			return Outcome{}, errors.New("filesystem.write requires string argument content")
		}
		if _, exists := state.Files[candidate.Target]; exists {
			diff.Modified = append(diff.Modified, candidate.Target)
		} else {
			diff.Created = append(diff.Created, candidate.Target)
		}
		state.Files[candidate.Target] = content
		state.MutationCount++
		addCompletedOperation(state, candidate.OperationClass)
		diff.CompletedOperation = candidate.OperationClass
	case "filesystem.delete":
		if _, exists := state.Files[candidate.Target]; !exists {
			return Outcome{}, fmt.Errorf("cannot delete absent path %s", candidate.Target)
		}
		delete(state.Files, candidate.Target)
		diff.Deleted = append(diff.Deleted, candidate.Target)
		state.MutationCount++
		addCompletedOperation(state, candidate.OperationClass)
		diff.CompletedOperation = candidate.OperationClass
	case "service.deploy":
		content, ok := candidate.Arguments["content"].(string)
		if !ok {
			return Outcome{}, errors.New("service.deploy requires string argument content")
		}
		if _, exists := state.Files[candidate.Target]; exists {
			diff.Modified = append(diff.Modified, candidate.Target)
		} else {
			diff.Created = append(diff.Created, candidate.Target)
		}
		state.Files[candidate.Target] = content
		state.MutationCount++
		addCompletedOperation(state, candidate.OperationClass)
		diff.CompletedOperation = candidate.OperationClass
	case "task.complete":
		state.TaskComplete = true
		addCompletedOperation(state, candidate.OperationClass)
		diff.CompletedOperation = candidate.OperationClass
	default:
		return Outcome{}, fmt.Errorf("unsupported operation class %q", candidate.OperationClass)
	}
	state.BenchmarkStep++
	state.ConstraintFeedback = nil
	sort.Strings(diff.Created)
	sort.Strings(diff.Modified)
	sort.Strings(diff.Deleted)
	return Outcome{Diff: diff, Mutation: mutating, Complete: state.TaskComplete}, nil
}

func addCompletedOperation(state *benchmark.State, operation string) {
	for _, existing := range state.CompletedOperations {
		if existing == operation {
			return
		}
	}
	state.CompletedOperations = append(state.CompletedOperations, operation)
	sort.Strings(state.CompletedOperations)
}
