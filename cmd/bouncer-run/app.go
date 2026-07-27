package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"bouncer/internal/benchmark"
	"bouncer/internal/calibration"
	"bouncer/internal/config"
	"bouncer/internal/control"
	"bouncer/internal/eventlog"
	"bouncer/internal/executor"
	"bouncer/internal/harness"
	"bouncer/internal/learning"
	"bouncer/internal/nimclient"
	"bouncer/internal/policy"
	"bouncer/internal/projector"
	"bouncer/internal/provider"
	"bouncer/internal/router"
	"bouncer/internal/telemetry"
)

func run(options runOptions) (runErr error) {
	rootContext := context.Background()
	shutdownTelemetry, err := telemetry.Setup(rootContext, telemetry.Config{
		ServiceName:    "bouncer-control-plane",
		ServiceVersion: "0.1.0",
		OTLPEndpoint:   options.OTLPEndpoint,
		SampleRatio:    options.TraceSampleRatio,
	})
	if err != nil {
		return err
	}
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTelemetry(shutdownContext); err != nil && runErr == nil {
			runErr = fmt.Errorf("shutdown telemetry: %w", err)
		}
	}()
	manifest, err := config.LoadManifest(options.ManifestPath)
	if err != nil {
		return err
	}
	task, err := benchmark.LoadTask(options.TaskPath)
	if err != nil {
		return err
	}
	if options.Endpoint != "" {
		manifest.Model.Endpoint = options.Endpoint
	}
	manifestHash, err := hashFile(options.ManifestPath)
	if err != nil {
		return err
	}
	taskHash, err := hashFile(options.TaskPath)
	if err != nil {
		return err
	}
	policyDAGPath := filepath.Join(options.ProjectRoot, "configs/skill_dag.json")
	policyHash, err := hashFile(policyDAGPath)
	if err != nil {
		return err
	}
	calibrationPath := options.ObjectiveCalibration
	if calibrationPath == "" {
		calibrationPath = "configs/objective-calibration.bootstrap.json"
	}
	if !filepath.IsAbs(calibrationPath) {
		calibrationPath = filepath.Join(options.ProjectRoot, calibrationPath)
	}
	objectiveCalibrator, err := calibration.Load(calibrationPath)
	if err != nil {
		return err
	}
	learningConfig := control.LearningConfig{
		Mode: options.LearningMode,
		Router: router.LearnedConfig{
			RiskCeiling:            options.LearningRiskCeiling,
			MaxRelativeUncertainty: options.LearningUncertainty,
			FrontierLimit:          options.LearningFrontierLimit,
		},
	}
	var learningScorer learning.Scorer
	if options.LearningMode != "" && options.LearningMode != control.LearningDisabled {
		learningPath := options.LearningArtifact
		if learningPath == "" {
			return fmt.Errorf("learning artifact is required when learning is enabled")
		}
		if !filepath.IsAbs(learningPath) {
			learningPath = filepath.Join(options.ProjectRoot, learningPath)
		}
		learningRuntime, loadErr := learning.Load(learningPath)
		if loadErr != nil {
			return loadErr
		}
		if options.LearningMode == control.LearningActive &&
			learningRuntime.Metadata().Provenance.Method == "hand_authored_bootstrap_not_for_promotion" {
			return fmt.Errorf("bootstrap learning artifact is restricted to shadow mode")
		}
		learningScorer = learningRuntime
	}
	seed := manifest.Benchmark.Seed
	if options.SeedOverride >= 0 {
		seed = options.SeedOverride
	}
	proposerCount := manifest.Proposal.ProposerCount
	if options.ProposerOverride != 0 {
		proposerCount = options.ProposerOverride
	}
	beamWidth := manifest.Proposal.BeamWidth
	if options.BeamOverride != 0 {
		beamWidth = options.BeamOverride
	}
	routerConfig := router.DefaultConfig()
	if options.RoutingStrategy != "" {
		routerConfig = router.Config{
			Strategy:    options.RoutingStrategy,
			RiskCeiling: options.RiskCeiling,
			Weights: router.Weights{
				LatencyMS:  options.LatencyWeight,
				CostUnits:  options.CostWeight,
				SafetyRisk: options.RiskWeight,
			},
			Epsilon: options.ExplorationEpsilon,
		}
	}
	if err := routerConfig.Validate(); err != nil {
		return fmt.Errorf("routing configuration: %w", err)
	}
	providerKind := options.ProviderKind
	if providerKind == "" {
		providerKind = provider.KindOpenAICompatible
	}
	proposalProvider, err := provider.New(provider.Config{
		Kind:       providerKind,
		ReplayPath: options.ReplayPath,
		BeamWidth:  beamWidth,
		HTTP: nimclient.Config{
			BaseURL:         manifest.Model.Endpoint,
			APIKey:          nimclient.APIKeyFromEnvironment(),
			Model:           manifest.Model.ID,
			ReasoningBudget: manifest.Model.ReasoningBudget,
			BudgetParameter: manifest.Model.BudgetParameter(),
			BeamWidth:       beamWidth,
			MaxTokens:       manifest.Model.MaxTokens,
			Temperature:     manifest.Model.Temperature,
			TopP:            manifest.Model.TopP,
			ReasoningEffort: manifest.Model.ReasoningEffort,
			MaxAttempts:     manifest.Retry.MaxAttempts,
			BaseDelay:       manifest.Retry.BaseDelay(),
			MaxDelay:        manifest.Retry.MaxDelay(),
			HTTPClient:      &http.Client{},
		},
	})
	if err != nil {
		return err
	}
	var batchProjector projector.BatchProjector
	switch options.PolicyEngine {
	case "go":
		goPolicy, err := policy.Load(policyDAGPath)
		if err != nil {
			return err
		}
		batchProjector = goPolicy
	case "python-subprocess":
		batchProjector = projector.PythonClient{
			WorkingDir: options.ProjectRoot,
			DAGPath:    "configs/skill_dag.json",
		}
	case "python-persistent":
		pythonProjector := projector.PythonClient{
			WorkingDir: options.ProjectRoot,
			DAGPath:    "configs/skill_dag.json",
		}
		persistent, err := projector.NewPersistentClient(pythonProjector)
		if err != nil {
			return err
		}
		defer func() {
			if err := persistent.Close(); err != nil {
				log.Printf("close persistent projector: %v", err)
			}
		}()
		batchProjector = persistent
	default:
		return fmt.Errorf("policy engine must be go, python-subprocess, or python-persistent")
	}
	var actionExecutor executor.Executor = executor.Virtual{}
	if options.ExecutorMode == "remote" {
		remote, err := executor.NewRemote(executor.RemoteConfig{
			BaseURL:           options.SandboxURL,
			Token:             os.Getenv("BOUNCER_SANDBOX_TOKEN"),
			AllowInsecureHTTP: options.AllowInsecureSandbox,
		})
		if err != nil {
			return err
		}
		actionExecutor = remote
	} else if options.ExecutorMode != "virtual" {
		return fmt.Errorf("executor mode must be virtual or remote")
	}
	var traceSink control.TraceSink
	var finalResult *control.Result
	if options.EventLogPath != "" {
		if err := os.MkdirAll(filepath.Dir(options.EventLogPath), 0o755); err != nil {
			return fmt.Errorf("create event log directory: %w", err)
		}
		eventOutput, err := os.OpenFile(
			options.EventLogPath,
			os.O_CREATE|os.O_EXCL|os.O_WRONLY,
			0o640,
		)
		if err != nil {
			return fmt.Errorf("create event log: %w", err)
		}
		runID, err := eventlog.NewID()
		if err != nil {
			eventOutput.Close()
			return err
		}
		logger := eventlog.NewWriter(eventOutput)
		if err := logger.Append(eventlog.Event{
			EventType: "run.started",
			RunID:     runID,
			TaskID:    task.TaskID,
			Seed:      seed,
			Payload: map[string]any{
				"model":                 manifest.Model.ID,
				"provider":              providerKind,
				"endpoint":              manifest.Model.Endpoint,
				"proposers":             proposerCount,
				"beam_width":            beamWidth,
				"policy_engine":         options.PolicyEngine,
				"executor_mode":         options.ExecutorMode,
				"manifest_sha256":       manifestHash,
				"task_sha256":           taskHash,
				"policy_sha256":         policyHash,
				"routing":               routerConfig,
				"objective_calibration": objectiveCalibrator.Metadata(),
				"learning": map[string]any{
					"configuration": learningConfig,
					"artifact": func() any {
						if learningScorer == nil {
							return nil
						}
						return learningScorer.Metadata()
					}(),
				},
				"adaptive_proposals": map[string]any{
					"enabled":        options.AdaptiveProposals,
					"initial_count":  options.InitialProposers,
					"minimum_valid":  options.MinimumValid,
					"minimum_spread": options.MinimumSpread,
				},
			},
		}); err != nil {
			eventOutput.Close()
			return err
		}
		traceSink = eventlog.TraceSink{
			Writer: logger,
			RunID:  runID,
			TaskID: task.TaskID,
			Seed:   seed,
		}
		defer func() {
			eventType := "run.failed"
			payload := map[string]any{}
			if runErr != nil {
				payload["error"] = runErr.Error()
			} else if finalResult != nil {
				eventType = "run.completed"
				payload = map[string]any{
					"passed":           finalResult.Passed,
					"task_complete":    finalResult.TaskComplete,
					"turns":            finalResult.Turns,
					"total_tokens":     finalResult.TotalTokens,
					"severe_mutations": finalResult.SevereMutations,
				}
			}
			if err := logger.Append(eventlog.Event{
				EventType: eventType,
				RunID:     runID,
				TaskID:    task.TaskID,
				Seed:      seed,
				Payload:   payload,
			}); err != nil && runErr == nil {
				runErr = err
			}
			if err := eventOutput.Close(); err != nil && runErr == nil {
				runErr = fmt.Errorf("close event log: %w", err)
			}
		}()
	}
	loop := control.Loop{
		Coordinator: harness.Coordinator{
			Proposer:      proposalProvider,
			ProposerCount: proposerCount,
			Timeout:       manifest.Proposal.Timeout(),
		},
		Projector:      batchProjector,
		Calibrator:     objectiveCalibrator,
		Executor:       actionExecutor,
		TraceSink:      traceSink,
		RouterConfig:   routerConfig,
		LearningScorer: learningScorer,
		Learning:       learningConfig,
		AdaptiveProposal: control.AdaptiveProposalConfig{
			Enabled:       options.AdaptiveProposals,
			InitialCount:  options.InitialProposers,
			MinimumValid:  options.MinimumValid,
			MinimumSpread: options.MinimumSpread,
		},
		MaxTurns: manifest.Benchmark.MaxTurns,
	}
	ctx, cancel := context.WithTimeout(rootContext, time.Duration(manifest.Benchmark.TaskTimeoutMS)*time.Millisecond)
	defer cancel()
	result, err := loop.Run(ctx, task, seed)
	if err != nil {
		return err
	}
	finalResult = &result
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode run result: %w", err)
	}
	if options.OutputPath != "" {
		if err := writeExclusive(options.OutputPath, append(encoded, '\n')); err != nil {
			return fmt.Errorf("write run result: %w", err)
		}
	}
	fmt.Println(string(encoded))
	return nil
}

func resolvePolicyEngine(engine, legacy string) string {
	if legacy == "" {
		return engine
	}
	if engine != "go" {
		return "conflicting-policy-engine-flags"
	}
	switch legacy {
	case "subprocess":
		return "python-subprocess"
	case "persistent":
		return "python-persistent"
	default:
		return "invalid-legacy-projector-mode"
	}
}

func writeExclusive(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create result directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
