package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"bouncer/internal/config"
	"bouncer/internal/eventlog"
	"bouncer/internal/harness"
	"bouncer/internal/nimclient"
)

type taskSpec struct {
	TaskID       string          `json:"task_id"`
	Instruction  string          `json:"instruction"`
	Policy       json.RawMessage `json:"policy"`
	InitialState json.RawMessage `json:"initial_state"`
}

func main() {
	manifestPath := flag.String("manifest", "configs/run-manifest.example.json", "path to the frozen run manifest")
	taskPath := flag.String("task", "benchmarks/tasks/task-001.json", "path to a smoke task")
	outputPath := flag.String("output", "", "JSONL event output path; defaults to a timestamped result file")
	flag.Parse()

	if err := run(*manifestPath, *taskPath, *outputPath); err != nil {
		log.Printf("bouncer harness failed: %v", err)
		os.Exit(1)
	}
}

func run(manifestPath, taskPath, outputPath string) (runErr error) {
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	task, err := loadTask(taskPath)
	if err != nil {
		return err
	}
	if outputPath == "" {
		outputPath = filepath.Join("benchmarks", "results", fmt.Sprintf("harness-%s.jsonl", time.Now().UTC().Format("20060102T150405.000000000Z")))
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create event directory: %w", err)
	}
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("create event log: %w", err)
	}
	defer output.Close()
	logger := eventlog.NewWriter(output)
	runID, err := eventlog.NewID()
	if err != nil {
		return err
	}
	if err := logger.Append(eventlog.Event{
		EventType: "run.started",
		RunID:     runID,
		TaskID:    task.TaskID,
		Seed:      manifest.Benchmark.Seed,
		Payload: map[string]any{
			"model":            manifest.Model.ID,
			"proposer_count":   manifest.Proposal.ProposerCount,
			"beam_width":       manifest.Proposal.BeamWidth,
			"proposal_timeout": manifest.Proposal.TimeoutMS,
		},
	}); err != nil {
		return err
	}
	defer func() {
		eventType := "run.completed"
		payload := map[string]any{}
		if runErr != nil {
			eventType = "run.failed"
			payload["error"] = runErr.Error()
		}
		if err := logger.Append(eventlog.Event{
			EventType: eventType,
			RunID:     runID,
			TaskID:    task.TaskID,
			Seed:      manifest.Benchmark.Seed,
			Payload:   payload,
		}); err != nil && runErr == nil {
			runErr = err
		}
	}()

	client, err := nimclient.New(nimclient.Config{
		BaseURL:         manifest.Model.Endpoint,
		APIKey:          nimclient.APIKeyFromEnvironment(),
		Model:           manifest.Model.ID,
		ReasoningBudget: manifest.Model.ReasoningBudget,
		BudgetParameter: manifest.Model.BudgetParameter(),
		BeamWidth:       manifest.Proposal.BeamWidth,
		MaxTokens:       manifest.Model.MaxTokens,
		Temperature:     manifest.Model.Temperature,
		TopP:            manifest.Model.TopP,
		ReasoningEffort: manifest.Model.ReasoningEffort,
		MaxAttempts:     manifest.Retry.MaxAttempts,
		BaseDelay:       manifest.Retry.BaseDelay(),
		MaxDelay:        manifest.Retry.MaxDelay(),
		HTTPClient:      &http.Client{},
	})
	if err != nil {
		return err
	}
	coordinator := harness.Coordinator{
		Proposer:      client,
		ProposerCount: manifest.Proposal.ProposerCount,
		Timeout:       manifest.Proposal.Timeout(),
	}

	if err := logger.Append(eventlog.Event{
		EventType: "proposal.requested",
		RunID:     runID,
		TaskID:    task.TaskID,
		StepID:    0,
		Seed:      manifest.Benchmark.Seed,
		Payload: map[string]any{
			"proposer_count": manifest.Proposal.ProposerCount,
			"beam_width":     manifest.Proposal.BeamWidth,
			"model":          manifest.Model.ID,
		},
	}); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(manifest.Benchmark.TaskTimeoutMS)*time.Millisecond)
	defer cancel()
	results, err := coordinator.ProposeAll(ctx, harness.Request{
		TaskID:      task.TaskID,
		Instruction: task.Instruction,
		State:       task.InitialState,
		Policy:      task.Policy,
		BaseSeed:    manifest.Benchmark.Seed,
	})
	if err != nil {
		_ = logger.Append(eventlog.Event{
			EventType: "proposal.failed",
			RunID:     runID,
			TaskID:    task.TaskID,
			StepID:    0,
			Seed:      manifest.Benchmark.Seed,
			Payload:   map[string]any{"error": err.Error()},
		})
		return err
	}
	for index, result := range results {
		if err := logger.Append(eventlog.Event{
			EventType: "proposal.completed",
			RunID:     runID,
			TaskID:    task.TaskID,
			StepID:    0,
			Attempt:   result.Attempts,
			Seed:      manifest.Benchmark.Seed + int64(index),
			Payload: map[string]any{
				"proposer_id":   result.ProposerID,
				"finish_reason": result.FinishReason,
				"latency_ms":    result.LatencyMS,
				"usage":         result.Usage,
				"actions":       result.Beam.Actions,
			},
		}); err != nil {
			return err
		}
	}

	summary := struct {
		RunID     string                     `json:"run_id"`
		TaskID    string                     `json:"task_id"`
		EventLog  string                     `json:"event_log"`
		Proposals []nimclient.ProposalResult `json:"proposals"`
	}{RunID: runID, TaskID: task.TaskID, EventLog: outputPath, Proposals: results}
	encoded, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func loadTask(path string) (taskSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return taskSpec{}, fmt.Errorf("read task: %w", err)
	}
	var envelope struct {
		TaskID       string          `json:"task_id"`
		Instruction  string          `json:"instruction"`
		Policy       json.RawMessage `json:"policy"`
		InitialState json.RawMessage `json:"initial_state"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&envelope); err != nil {
		return taskSpec{}, fmt.Errorf("decode task: %w", err)
	}
	if envelope.TaskID == "" || envelope.Instruction == "" || len(envelope.Policy) == 0 || len(envelope.InitialState) == 0 {
		return taskSpec{}, errors.New("task id, instruction, policy, and initial state are required")
	}
	return taskSpec(envelope), nil
}
