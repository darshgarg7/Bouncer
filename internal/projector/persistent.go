package projector

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
)

type PersistentClient struct {
	mutex   sync.Mutex
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	stderr  *lockedBuffer
	closed  bool
}

type lockedBuffer struct {
	mutex sync.Mutex
	data  strings.Builder
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.data.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.data.String()
}

func NewPersistentClient(config PythonClient) (*PersistentClient, error) {
	python := config.GetPythonCmd()
	dagPath := config.DAGPath
	if dagPath == "" {
		dagPath = "configs/skill_dag.json"
	}
	command := exec.Command(
		python,
		"-m",
		"constraint_projection",
		"--dag",
		dagPath,
		"--format",
		"json",
		"--stream",
	)
	command.Dir = config.WorkingDir
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open persistent projector stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("open persistent projector stdout: %w", err)
	}
	stderr := &lockedBuffer{}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		stdin.Close()
		return nil, fmt.Errorf("start persistent projector: %w", err)
	}
	return &PersistentClient{
		command: command,
		stdin:   stdin,
		stdout:  bufio.NewReader(stdout),
		stderr:  stderr,
	}, nil
}

func (c *PersistentClient) Evaluate(
	ctx context.Context,
	actions []action.Candidate,
	state benchmark.State,
	policy benchmark.Policy,
) ([]Result, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return nil, errors.New("persistent projector is closed")
	}
	if len(actions) == 0 {
		return nil, errors.New("projector requires at least one action")
	}
	envelope := struct {
		Actions []action.Candidate `json:"actions"`
		State   benchmark.State    `json:"state"`
		Policy  benchmark.Policy   `json:"policy"`
	}{Actions: actions, State: state, Policy: policy}
	input, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode persistent projection batch: %w", err)
	}
	input = append(input, '\n')
	if _, err := c.stdin.Write(input); err != nil {
		return nil, fmt.Errorf(
			"write persistent projection batch: %w: %s",
			err,
			strings.TrimSpace(c.stderr.String()),
		)
	}
	type readResult struct {
		data []byte
		err  error
	}
	readChannel := make(chan readResult, 1)
	go func() {
		data, err := c.stdout.ReadBytes('\n')
		readChannel <- readResult{data: data, err: err}
	}()
	var line []byte
	select {
	case <-ctx.Done():
		_ = c.command.Process.Kill()
		return nil, fmt.Errorf("persistent projection canceled: %w", ctx.Err())
	case result := <-readChannel:
		if result.err != nil {
			return nil, fmt.Errorf(
				"read persistent projection batch: %w: %s",
				result.err,
				strings.TrimSpace(c.stderr.String()),
			)
		}
		line = result.data
	}
	var response struct {
		Results []Result `json:"results"`
		Error   string   `json:"error"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode persistent projection batch: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode persistent projection batch: invalid trailing content")
	}
	if response.Error != "" {
		return nil, fmt.Errorf("persistent projector rejected batch: %s", response.Error)
	}
	if len(response.Results) != len(actions) {
		return nil, fmt.Errorf(
			"persistent projector returned %d results for %d actions",
			len(response.Results),
			len(actions),
		)
	}
	for index, result := range response.Results {
		if result.ActionID != actions[index].CandidateID {
			return nil, fmt.Errorf(
				"persistent projector result %d action id %q does not match %q",
				index,
				result.ActionID,
				actions[index].CandidateID,
			)
		}
	}
	return response.Results, nil
}

func (c *PersistentClient) Close() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if err := c.stdin.Close(); err != nil {
		_ = c.command.Process.Kill()
		_ = c.command.Wait()
		return fmt.Errorf("close persistent projector stdin: %w", err)
	}
	if err := c.command.Wait(); err != nil {
		return fmt.Errorf(
			"wait for persistent projector: %w: %s",
			err,
			strings.TrimSpace(c.stderr.String()),
		)
	}
	return nil
}
