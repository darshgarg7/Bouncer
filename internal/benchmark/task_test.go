package benchmark

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func completeTaskFixture() Task {
	return Task{
		SchemaVersion: "0.1.0",
		TaskID:        "task-001",
		Title:         "fixture",
		Instruction:   "perform the fixture",
		Policy: Policy{
			AllowedOperationClasses: []string{"filesystem.write", "task.complete"},
			AllowedPathPrefixes:     []string{"workspace/"},
			ProtectedPaths:          []string{"workspace/protected.txt"},
			MaxMutations:            1,
		},
		InitialState: InitialState{
			CompletedOperations: []string{"state.validate", "filesystem.read"},
			Files: map[string]string{
				"workspace/input.txt":     "input",
				"workspace/protected.txt": "fixed",
			},
		},
		Oracle: Oracle{
			RequiredFiles:  map[string]string{"workspace/output.txt": "output"},
			AbsentPaths:    []string{"workspace/forbidden.txt"},
			UnchangedPaths: []string{"workspace/protected.txt"},
		},
	}
}

func writeTaskFixture(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "task.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestLoadTaskAcceptsStrictValidDocument(t *testing.T) {
	want := completeTaskFixture()
	got, err := LoadTask(writeTaskFixture(t, want))
	if err != nil {
		t.Fatalf("LoadTask returned error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded task mismatch:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestLoadTaskRejectsUnreadableUnknownAndTrailingDocuments(t *testing.T) {
	if _, err := LoadTask(filepath.Join(t.TempDir(), "missing.json")); err == nil || !strings.Contains(err.Error(), "read task") {
		t.Fatalf("got missing-file error %v", err)
	}

	unknown := map[string]any{}
	encoded, err := json.Marshal(completeTaskFixture())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["unexpected"] = true
	if _, err := LoadTask(writeTaskFixture(t, unknown)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("got unknown-field error %v", err)
	}

	path := writeTaskFixture(t, completeTaskFixture())
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(` {"second":true}`); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTask(path); err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("got trailing-document error %v", err)
	}
}

func TestTaskValidateRejectsIncompleteContracts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Task)
		want   string
	}{
		{name: "identity", mutate: func(task *Task) { task.TaskID = "" }, want: "required"},
		{name: "operations", mutate: func(task *Task) { task.Policy.AllowedOperationClasses = nil }, want: "requires operations"},
		{name: "paths", mutate: func(task *Task) { task.Policy.AllowedPathPrefixes = nil }, want: "requires operations"},
		{name: "mutations", mutate: func(task *Task) { task.Policy.MaxMutations = -1 }, want: "must not be negative"},
		{name: "initial files", mutate: func(task *Task) { task.InitialState.Files = nil }, want: "file maps"},
		{name: "oracle files", mutate: func(task *Task) { task.Oracle.RequiredFiles = nil }, want: "file maps"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := completeTaskFixture()
			test.mutate(&task)
			if err := task.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got error %v", err)
			}
		})
	}
}

func TestNewStateClonesFilesAndSortsOperations(t *testing.T) {
	task := completeTaskFixture()
	state := task.NewState()
	if !reflect.DeepEqual(state.CompletedOperations, []string{"filesystem.read", "state.validate"}) {
		t.Fatalf("operations are not sorted: %v", state.CompletedOperations)
	}
	state.Files["workspace/input.txt"] = "changed"
	if task.InitialState.Files["workspace/input.txt"] != "input" {
		t.Fatal("NewState aliased the task's initial file map")
	}
	if state.ConstraintFeedback == nil {
		t.Fatal("constraint feedback must initialize as an empty array")
	}
}

func TestEvaluateReportsAllOracleFailureClassesInStableOrder(t *testing.T) {
	task := completeTaskFixture()
	state := task.NewState()
	state.Files["workspace/forbidden.txt"] = "present"
	state.Files["workspace/protected.txt"] = "changed"
	result := task.Evaluate(state)
	if result.Passed {
		t.Fatal("oracle unexpectedly passed")
	}
	want := []string{
		"path workspace/forbidden.txt should be absent",
		"path workspace/protected.txt changed",
		"required file workspace/output.txt is absent",
	}
	if !reflect.DeepEqual(result.Failures, want) {
		t.Fatalf("got failures %v, want %v", result.Failures, want)
	}
	state.Files["workspace/output.txt"] = "wrong"
	result = task.Evaluate(state)
	if !strings.Contains(strings.Join(result.Failures, " "), "unexpected content") {
		t.Fatalf("missing content mismatch: %v", result.Failures)
	}
	state.Files["workspace/output.txt"] = "output"
	delete(state.Files, "workspace/forbidden.txt")
	state.Files["workspace/protected.txt"] = "fixed"
	if result = task.Evaluate(state); !result.Passed || len(result.Failures) != 0 {
		t.Fatalf("valid state failed: %+v", result)
	}
}

func TestNewStateSerializesEmptyCollectionsAsArrays(t *testing.T) {
	task := Task{
		InitialState: InitialState{CompletedOperations: []string{}, Files: map[string]string{}},
	}
	encoded, err := task.NewState().JSON()
	if err != nil {
		t.Fatalf("JSON returned error: %v", err)
	}
	if !strings.Contains(string(encoded), `"completed_operations":[]`) {
		t.Fatalf("completed_operations was not an array: %s", encoded)
	}
}
