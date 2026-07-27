package projector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
	"bouncer/internal/policy"
)

type Violation = policy.Violation
type Result = policy.Result
type BatchProjector = policy.BatchEvaluator

type PythonClient struct {
	Python     string
	WorkingDir string
	DAGPath    string
}

func (c PythonClient) Evaluate(ctx context.Context, actions []action.Candidate, state benchmark.State, policy benchmark.Policy) ([]Result, error) {
	if len(actions) == 0 {
		return nil, errors.New("projector requires at least one action")
	}
	python := c.Python
	if python == "" {
		python = "python3"
	}
	dagPath := c.DAGPath
	if dagPath == "" {
		dagPath = "configs/skill_dag.json"
	}
	envelope := struct {
		Actions []action.Candidate `json:"actions"`
		State   benchmark.State    `json:"state"`
		Policy  benchmark.Policy   `json:"policy"`
	}{Actions: actions, State: state, Policy: policy}
	input, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode projection batch: %w", err)
	}

	command := exec.CommandContext(ctx, python, "-m", "constraint_projection", "--dag", dagPath, "--format", "json")
	command.Dir = c.WorkingDir
	command.Stdin = bytes.NewReader(input)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("run constraint projector: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var response struct {
		Results []Result `json:"results"`
	}
	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode projection batch: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode projection batch: invalid trailing content")
	}
	if len(response.Results) != len(actions) {
		return nil, fmt.Errorf("projector returned %d results for %d actions", len(response.Results), len(actions))
	}
	for index, result := range response.Results {
		if result.ActionID != actions[index].CandidateID {
			return nil, fmt.Errorf("projector result %d action id %q does not match %q", index, result.ActionID, actions[index].CandidateID)
		}
	}
	return response.Results, nil
}
