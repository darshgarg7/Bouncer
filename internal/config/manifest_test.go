package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadExampleManifest(t *testing.T) {
	manifest, err := LoadManifest("../../configs/run-manifest.example.json")
	if err != nil {
		t.Fatalf("LoadManifest returned error: %v", err)
	}
	if manifest.Proposal.ProposerCount != 1 || manifest.Proposal.BeamWidth != 1 {
		t.Fatalf("unexpected proposal contract: %+v", manifest.Proposal)
	}
	if manifest.Model.MaxTokens <= manifest.Model.ReasoningBudget {
		t.Fatal("example max_tokens does not leave room after reasoning budget")
	}
}

func TestLoadFrozenSyntheticManifest(t *testing.T) {
	manifest, err := LoadManifest("../../configs/run-manifest.synthetic-v1.json")
	if err != nil {
		t.Fatalf("LoadManifest returned error: %v", err)
	}
	if manifest.Proposal.ProposerCount != 3 || manifest.Proposal.BeamWidth != 5 {
		t.Fatalf("unexpected frozen proposal contract: %+v", manifest.Proposal)
	}
}

func TestLoadNVIDIAHostedManifest(t *testing.T) {
	manifest, err := LoadManifest("../../configs/run-manifest.nvidia-hosted.json")
	if err != nil {
		t.Fatalf("LoadManifest returned error: %v", err)
	}
	if manifest.Model.ReasoningEffort != "medium" || manifest.Model.TopP == nil || *manifest.Model.TopP != 0.95 {
		t.Fatalf("unexpected hosted model settings: %+v", manifest.Model)
	}
	if manifest.Model.BudgetParameter() != "reasoning_budget" || manifest.Proposal.TimeoutMS != 240000 {
		t.Fatalf("unexpected hosted provider contract: %+v", manifest)
	}
}

func TestManifestRejectsProposalCountsOutsideBounds(t *testing.T) {
	for _, count := range []int{-1, 0, 17} {
		manifest := validManifest()
		manifest.Proposal.ProposerCount = count
		if err := manifest.Validate(); err == nil {
			t.Fatalf("Validate accepted proposer count %d", count)
		}
		manifest = validManifest()
		manifest.Proposal.BeamWidth = count
		if err := manifest.Validate(); err == nil {
			t.Fatalf("Validate accepted beam width %d", count)
		}
	}
}

func TestManifestRejectsReasoningBudgetAtTotalLimit(t *testing.T) {
	manifest := validManifest()
	manifest.Model.ReasoningBudget = manifest.Model.MaxTokens
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate returned nil when max_tokens equals reasoning_budget")
	}
}

func TestManifestRejectsUnimplementedTransport(t *testing.T) {
	manifest := validManifest()
	manifest.Benchmark.Transport = "kafka"
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate accepted an unimplemented transport")
	}
}

func TestModelBudgetParameterDefaultsAndValidatesProviderDialects(t *testing.T) {
	manifest := validManifest()
	if got := manifest.Model.BudgetParameter(); got != "thinking_token_budget" {
		t.Fatalf("got default budget parameter %q", got)
	}
	manifest.Model.ReasoningBudgetParameter = "reasoning_budget"
	if err := manifest.Validate(); err != nil {
		t.Fatalf("hosted budget parameter was rejected: %v", err)
	}
	if got := manifest.Model.BudgetParameter(); got != "reasoning_budget" {
		t.Fatalf("got hosted budget parameter %q", got)
	}
	manifest.Model.ReasoningBudgetParameter = "unknown"
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate accepted an unknown budget parameter")
	}
}

func TestModelSamplingAndReasoningSettingsAreBounded(t *testing.T) {
	topP := 0.95
	manifest := validManifest()
	manifest.Model.TopP = &topP
	manifest.Model.ReasoningEffort = "medium"
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate rejected hosted model settings: %v", err)
	}
	invalidTopP := 1.01
	manifest.Model.TopP = &invalidTopP
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate accepted top_p above one")
	}
	manifest = validManifest()
	manifest.Model.ReasoningEffort = "balanced"
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate accepted an unknown reasoning effort")
	}
}

func TestLoadManifestAndDurationFailurePaths(t *testing.T) {
	if _, err := LoadManifest(filepath.Join(t.TempDir(), "missing")); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("missing manifest returned %v", err)
	}
	for name, content := range map[string]string{
		"unknown":  `{"unexpected":true}`,
		"trailing": `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadManifest(path); err == nil {
				t.Fatal("LoadManifest accepted malformed document")
			}
		})
	}
	if got := (ProposalConfig{TimeoutMS: 12}).Timeout(); got != 12*time.Millisecond {
		t.Fatalf("proposal timeout=%v", got)
	}
	if got := (RetryConfig{BaseDelayMS: 13}).BaseDelay(); got != 13*time.Millisecond {
		t.Fatalf("base delay=%v", got)
	}
	if got := (RetryConfig{MaxDelayMS: 14}).MaxDelay(); got != 14*time.Millisecond {
		t.Fatalf("max delay=%v", got)
	}
}

func TestManifestRejectsEveryConfigurationBoundary(t *testing.T) {
	tests := map[string]func(*Manifest){
		"versions":      func(value *Manifest) { value.SchemaVersion = "old" },
		"model ID":      func(value *Manifest) { value.Model.ID = " " },
		"endpoint":      func(value *Manifest) { value.Model.Endpoint = " " },
		"budget":        func(value *Manifest) { value.Model.ReasoningBudget = -1 },
		"temperature":   func(value *Manifest) { value.Model.Temperature = 3 },
		"timeout":       func(value *Manifest) { value.Proposal.TimeoutMS = 0 },
		"retry count":   func(value *Manifest) { value.Retry.MaxAttempts = 0 },
		"retry delay":   func(value *Manifest) { value.Retry.BaseDelayMS = -1 },
		"retry maximum": func(value *Manifest) { value.Retry.MaxDelayMS = -1 },
		"seed":          func(value *Manifest) { value.Benchmark.Seed = -1 },
		"turns":         func(value *Manifest) { value.Benchmark.MaxTurns = 0 },
		"task timeout":  func(value *Manifest) { value.Benchmark.TaskTimeoutMS = 0 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			manifest := validManifest()
			mutate(&manifest)
			if err := manifest.Validate(); err == nil {
				t.Fatal("Validate accepted malformed manifest")
			}
		})
	}
}

func validManifest() Manifest {
	return Manifest{
		SchemaVersion:    "0.1.0",
		BenchmarkVersion: "0.1.0",
		Model: ModelConfig{
			ID:              "model",
			Endpoint:        "http://localhost/v1",
			ReasoningBudget: 1024,
			MaxTokens:       1536,
			Temperature:     0.7,
		},
		Proposal: ProposalConfig{ProposerCount: 3, BeamWidth: 5, TimeoutMS: 1},
		Retry:    RetryConfig{MaxAttempts: 1},
		Benchmark: BenchmarkConfig{
			Seed:          1,
			MaxTurns:      1,
			TaskTimeoutMS: 1,
			Transport:     "in_process",
		},
	}
}
