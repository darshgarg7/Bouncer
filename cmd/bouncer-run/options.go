package main

import (
	"flag"

	"bouncer/internal/control"
	"bouncer/internal/provider"
	"bouncer/internal/router"
)

type runOptions struct {
	ManifestPath          string
	TaskPath              string
	Endpoint              string
	ProjectRoot           string
	OutputPath            string
	EventLogPath          string
	SeedOverride          int64
	ProposerOverride      int
	BeamOverride          int
	PolicyEngine          string
	ExecutorMode          string
	SandboxURL            string
	AllowInsecureSandbox  bool
	RoutingStrategy       string
	RiskCeiling           float64
	LatencyWeight         float64
	CostWeight            float64
	RiskWeight            float64
	AdaptiveProposals     bool
	InitialProposers      int
	MinimumValid          int
	MinimumSpread         float64
	ProviderKind          string
	ReplayPath            string
	ExplorationEpsilon    float64
	OTLPEndpoint          string
	TraceSampleRatio      float64
	ObjectiveCalibration  string
	LearningMode          string
	LearningArtifact      string
	LearningRiskCeiling   float64
	LearningUncertainty   float64
	LearningFrontierLimit int
	AnomalyMode           string
	AnomalyArtifact       string
}

func parseOptions(arguments []string) (runOptions, error) {
	options := runOptions{}
	flags := flag.NewFlagSet("bouncer-run", flag.ContinueOnError)
	flags.StringVar(&options.ManifestPath, "manifest", "configs/run-manifest.example.json", "path to run manifest")
	flags.StringVar(&options.TaskPath, "task", "benchmarks/tasks/task-001.json", "path to task")
	flags.StringVar(&options.Endpoint, "endpoint", "", "override manifest model endpoint")
	flags.StringVar(&options.ProjectRoot, "project-root", ".", "repository root used by policy and calibration assets")
	flags.StringVar(&options.OutputPath, "output", "", "optional result JSON path")
	flags.StringVar(&options.EventLogPath, "event-log", "", "new append-only JSONL trace path")
	flags.Int64Var(&options.SeedOverride, "seed", -1, "override manifest seed")
	flags.IntVar(&options.ProposerOverride, "proposers", 0, "override proposer count for an ablation")
	flags.IntVar(&options.BeamOverride, "beam-width", 0, "override beam width for an ablation")
	policyEngine := flags.String("policy-engine", "go", "policy engine: go, python-subprocess, or python-persistent")
	legacyProjector := flags.String("projector-mode", "", "deprecated Python projector lifecycle: subprocess or persistent")
	flags.StringVar(&options.ExecutorMode, "executor-mode", "virtual", "executor backend: virtual or remote")
	flags.StringVar(&options.SandboxURL, "sandbox-url", "", "remote sandbox base URL")
	flags.BoolVar(&options.AllowInsecureSandbox, "allow-insecure-sandbox", false, "allow HTTP sandbox transport for local testing")
	flags.StringVar(
		&options.RoutingStrategy,
		"routing-strategy",
		router.StrategyLexicographic,
		"routing policy: first_valid, lexicographic, weighted_utility, pareto_utility, random_safe, epsilon_pareto, or legacy_crowding",
	)
	flags.Float64Var(&options.RiskCeiling, "risk-ceiling", 1, "maximum eligible calibrated safety risk in [0,1]")
	flags.Float64Var(&options.LatencyWeight, "latency-weight", 0.2, "normalized latency weight for utility routing")
	flags.Float64Var(&options.CostWeight, "cost-weight", 0.2, "normalized cost weight for utility routing")
	flags.Float64Var(&options.RiskWeight, "risk-weight", 0.6, "normalized safety-risk weight for utility routing")
	flags.BoolVar(&options.AdaptiveProposals, "adaptive-proposals", false, "start small and expand on weak candidate sets")
	flags.IntVar(&options.InitialProposers, "initial-proposers", 1, "initial proposer count for adaptive proposals")
	flags.IntVar(&options.MinimumValid, "minimum-valid", 2, "valid candidates required before skipping expansion")
	flags.Float64Var(&options.MinimumSpread, "minimum-spread", 0.25, "objective spread required before skipping expansion")
	flags.StringVar(&options.ProviderKind, "provider", provider.KindOpenAICompatible, "proposal provider: openai-compatible or recorded")
	flags.StringVar(&options.ReplayPath, "replay-file", "", "recorded provider replay JSON path")
	flags.Float64Var(&options.ExplorationEpsilon, "exploration-epsilon", 0.05, "exploration rate for epsilon_pareto routing")
	flags.StringVar(&options.OTLPEndpoint, "otlp-endpoint", "", "optional OTLP/HTTP traces endpoint")
	flags.Float64Var(&options.TraceSampleRatio, "trace-sample-ratio", 1, "OpenTelemetry trace sample ratio in [0,1]")
	flags.StringVar(
		&options.ObjectiveCalibration,
		"objective-calibration",
		"configs/objective-calibration.bootstrap.json",
		"path to the trusted objective calibration artifact",
	)
	flags.StringVar(&options.LearningMode, "learning-mode", control.LearningDisabled, "learned routing promotion: disabled, shadow, or active")
	flags.StringVar(&options.LearningArtifact, "learning-artifact", "configs/learning-artifact.bootstrap.json", "portable learning artifact; bootstrap is shadow-only")
	flags.Float64Var(&options.LearningRiskCeiling, "learning-risk-ceiling", 0.25, "maximum conservative learned adverse risk in [0,1]")
	flags.Float64Var(&options.LearningUncertainty, "learning-max-relative-uncertainty", 0.5, "maximum relative learned uncertainty")
	flags.IntVar(&options.LearningFrontierLimit, "learning-frontier-limit", 16, "maximum learned Pareto-front candidates")
	flags.StringVar(&options.AnomalyMode, "anomaly-mode", control.AnomalyDisabled, "static anomaly circuit breaker: disabled, shadow, or active")
	flags.StringVar(&options.AnomalyArtifact, "anomaly-artifact", "configs/anomaly-artifact.bootstrap.json", "immutable Isolation Forest artifact; bootstrap is shadow-only")
	if err := flags.Parse(arguments); err != nil {
		return runOptions{}, err
	}
	options.PolicyEngine = resolvePolicyEngine(*policyEngine, *legacyProjector)
	return options, nil
}
