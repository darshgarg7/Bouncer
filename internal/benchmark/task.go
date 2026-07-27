package benchmark

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

type Policy struct {
	AllowedOperationClasses []string `json:"allowed_operation_classes"`
	AllowedPathPrefixes     []string `json:"allowed_path_prefixes"`
	ProtectedPaths          []string `json:"protected_paths"`
	MaxMutations            int      `json:"max_mutations"`
}

type InitialState struct {
	CompletedOperations []string          `json:"completed_operations"`
	Files               map[string]string `json:"files"`
}

type State struct {
	CompletedOperations []string          `json:"completed_operations"`
	Files               map[string]string `json:"files"`
	MutationCount       int               `json:"mutation_count"`
	BenchmarkStep       int               `json:"benchmark_step"`
	HazardInjected      bool              `json:"hazard_injected"`
	TaskComplete        bool              `json:"task_complete"`
	ConstraintFeedback  []string          `json:"constraint_feedback,omitempty"`
}

type Oracle struct {
	RequiredFiles  map[string]string `json:"required_files"`
	AbsentPaths    []string          `json:"absent_paths"`
	UnchangedPaths []string          `json:"unchanged_paths"`
}

type Task struct {
	SchemaVersion string       `json:"schema_version"`
	TaskID        string       `json:"task_id"`
	Title         string       `json:"title"`
	Instruction   string       `json:"instruction"`
	Policy        Policy       `json:"policy"`
	InitialState  InitialState `json:"initial_state"`
	Oracle        Oracle       `json:"oracle"`
}

type OracleResult struct {
	Passed   bool     `json:"passed"`
	Failures []string `json:"failures"`
}

func LoadTask(path string) (Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Task{}, fmt.Errorf("read task: %w", err)
	}
	var task Task
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&task); err != nil {
		return Task{}, fmt.Errorf("decode task: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Task{}, errors.New("decode task: trailing JSON value")
		}
		return Task{}, fmt.Errorf("decode task trailing content: %w", err)
	}
	if err := task.Validate(); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (t Task) Validate() error {
	if t.SchemaVersion != "0.1.0" || t.TaskID == "" || t.Instruction == "" {
		return errors.New("task schema_version, task_id, and instruction are required")
	}
	if len(t.Policy.AllowedOperationClasses) == 0 || len(t.Policy.AllowedPathPrefixes) == 0 {
		return errors.New("task policy requires operations and path prefixes")
	}
	if t.Policy.MaxMutations < 0 {
		return errors.New("task max_mutations must not be negative")
	}
	if t.InitialState.Files == nil || t.Oracle.RequiredFiles == nil {
		return errors.New("task initial and oracle file maps are required")
	}
	return nil
}

func (t Task) NewState() State {
	state := State{
		CompletedOperations: append([]string{}, t.InitialState.CompletedOperations...),
		Files:               cloneFiles(t.InitialState.Files),
		ConstraintFeedback:  []string{},
	}
	sort.Strings(state.CompletedOperations)
	return state
}

func (t Task) Evaluate(state State) OracleResult {
	failures := make([]string, 0)
	for path, expected := range t.Oracle.RequiredFiles {
		actual, ok := state.Files[path]
		if !ok {
			failures = append(failures, fmt.Sprintf("required file %s is absent", path))
			continue
		}
		if actual != expected {
			failures = append(failures, fmt.Sprintf("required file %s has unexpected content", path))
		}
	}
	for _, path := range t.Oracle.AbsentPaths {
		if _, ok := state.Files[path]; ok {
			failures = append(failures, fmt.Sprintf("path %s should be absent", path))
		}
	}
	for _, path := range t.Oracle.UnchangedPaths {
		initial, initiallyPresent := t.InitialState.Files[path]
		current, currentlyPresent := state.Files[path]
		if initiallyPresent != currentlyPresent || initial != current {
			failures = append(failures, fmt.Sprintf("path %s changed", path))
		}
	}
	sort.Strings(failures)
	return OracleResult{Passed: len(failures) == 0, Failures: failures}
}

func (s State) JSON() (json.RawMessage, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encode benchmark state: %w", err)
	}
	return data, nil
}

func cloneFiles(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for path, content := range source {
		result[path] = content
	}
	return result
}
