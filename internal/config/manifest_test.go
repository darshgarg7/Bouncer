package config

import "testing"

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
