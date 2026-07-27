package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"bouncer/internal/benchmark"
	"bouncer/internal/config"
	"bouncer/internal/harness"
	"bouncer/internal/nimclient"
	"bouncer/internal/providergate"
)

func main() {
	manifestPath := flag.String("manifest", "configs/run-manifest.example.json", "path to run manifest")
	taskPath := flag.String("task", "benchmarks/tasks/task-001.json", "path to connectivity task")
	endpoint := flag.String("endpoint", "", "override manifest model endpoint")
	outputDir := flag.String("output-dir", "", "new directory for immutable gate artifacts")
	batches := flag.Int("batches", 100, "number of three-call batches")
	seed := flag.Int64("seed", -1, "override manifest seed")
	flag.Parse()
	if err := run(*manifestPath, *taskPath, *endpoint, *outputDir, *batches, *seed); err != nil {
		log.Printf("bouncer provider gate failed: %v", err)
		os.Exit(1)
	}
}

func run(manifestPath, taskPath, endpoint, outputDir string, batches int, seedOverride int64) error {
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	task, err := benchmark.LoadTask(taskPath)
	if err != nil {
		return err
	}
	if endpoint != "" {
		manifest.Model.Endpoint = endpoint
	}
	seed := manifest.Benchmark.Seed
	if seedOverride >= 0 {
		seed = seedOverride
	}
	client, err := nimclient.New(nimclient.Config{
		BaseURL:         manifest.Model.Endpoint,
		APIKey:          os.Getenv("NIM_API_KEY"),
		Model:           manifest.Model.ID,
		ReasoningBudget: manifest.Model.ReasoningBudget,
		BudgetParameter: manifest.Model.BudgetParameter(),
		BeamWidth:       manifest.Proposal.BeamWidth,
		MaxTokens:       manifest.Model.MaxTokens,
		Temperature:     manifest.Model.Temperature,
		MaxAttempts:     manifest.Retry.MaxAttempts,
		BaseDelay:       manifest.Retry.BaseDelay(),
		MaxDelay:        manifest.Retry.MaxDelay(),
		HTTPClient:      &http.Client{},
	})
	if err != nil {
		return err
	}
	if outputDir == "" {
		outputDir = filepath.Join(
			"benchmarks",
			"results",
			"provider-gate-"+time.Now().UTC().Format("20060102T150405.000000000Z"),
		)
	}
	if err := createNewDirectory(outputDir); err != nil {
		return err
	}
	rawPath := filepath.Join(outputDir, "batches.jsonl")
	raw, err := os.OpenFile(rawPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("create raw gate artifact: %w", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	state, err := task.NewState().JSON()
	if err != nil {
		raw.Close()
		return err
	}
	runner := providergate.Runner{
		Coordinator: harness.Coordinator{
			Proposer:      client,
			ProposerCount: manifest.Proposal.ProposerCount,
			Timeout:       manifest.Proposal.Timeout(),
		},
		Batches: batches,
	}
	summary, runErr := runner.Run(ctx, harness.Request{
		TaskID:      task.TaskID,
		Instruction: task.Instruction,
		State:       state,
		BaseSeed:    seed,
	}, raw)
	closeErr := raw.Close()
	if runErr != nil {
		return runErr
	}
	if closeErr != nil {
		return fmt.Errorf("close raw gate artifact: %w", closeErr)
	}
	summary.ModelID = manifest.Model.ID
	summary.Endpoint = manifest.Model.Endpoint
	summary.BudgetParameter = manifest.Model.BudgetParameter()
	summary.ManifestSHA256, err = fileSHA256(manifestPath)
	if err != nil {
		return err
	}
	summary.TaskSHA256, err = fileSHA256(taskPath)
	if err != nil {
		return err
	}
	summary.RawArtifact = "batches.jsonl"
	summaryPath := filepath.Join(outputDir, "summary.json")
	if err := writeJSONExclusive(summaryPath, summary); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	if !summary.Passed {
		return fmt.Errorf(
			"gate did not pass: %d/%d batches completed, %d failed",
			summary.CompletedBatches,
			summary.RequestedBatches,
			summary.FailedBatches,
		)
	}
	return nil
}

func createNewDirectory(path string) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create provider gate parent directory: %w", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("provider gate output directory already exists: %s", path)
		}
		return fmt.Errorf("create provider gate output directory: %w", err)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func writeJSONExclusive(path string, value any) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		file.Close()
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}
