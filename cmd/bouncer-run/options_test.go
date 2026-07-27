package main

import (
	"errors"
	"flag"
	"testing"

	"bouncer/internal/control"
	"bouncer/internal/provider"
	"bouncer/internal/router"
)

func TestParseOptionsUsesSafeDefaults(t *testing.T) {
	options, err := parseOptions(nil)
	if err != nil {
		t.Fatalf("parseOptions returned %v", err)
	}
	if options.PolicyEngine != "go" || options.ExecutorMode != "virtual" {
		t.Fatalf("unexpected runtime defaults: %+v", options)
	}
	if options.ProviderKind != provider.KindOpenAICompatible ||
		options.RoutingStrategy != router.StrategyLexicographic ||
		options.LearningMode != control.LearningDisabled ||
		options.AnomalyMode != control.AnomalyDisabled {
		t.Fatalf("unexpected provider/routing defaults: %+v", options)
	}
}

func TestParseOptionsPreservesExplicitOverrides(t *testing.T) {
	options, err := parseOptions([]string{
		"-provider", "recorded",
		"-replay-file", "fixture.json",
		"-learning-mode", "shadow",
		"-anomaly-mode", "shadow",
		"-anomaly-artifact", "anomaly.json",
		"-beam-width", "3",
	})
	if err != nil {
		t.Fatalf("parseOptions returned %v", err)
	}
	if options.ProviderKind != "recorded" || options.ReplayPath != "fixture.json" ||
		options.LearningMode != "shadow" || options.AnomalyMode != "shadow" ||
		options.AnomalyArtifact != "anomaly.json" || options.BeamOverride != 3 {
		t.Fatalf("explicit overrides were not retained: %+v", options)
	}
}

func TestParseOptionsRejectsMalformedFlagValue(t *testing.T) {
	if _, err := parseOptions([]string{"-beam-width", "many"}); err == nil {
		t.Fatal("parseOptions accepted a malformed integer")
	}
}

func TestParseOptionsReturnsHelpSentinel(t *testing.T) {
	_, err := parseOptions([]string{"-help"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("parseOptions help returned %v", err)
	}
}
