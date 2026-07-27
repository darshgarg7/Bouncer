package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type Manifest struct {
	SchemaVersion    string          `json:"schema_version"`
	BenchmarkVersion string          `json:"benchmark_version"`
	Model            ModelConfig     `json:"model"`
	Proposal         ProposalConfig  `json:"proposal"`
	Retry            RetryConfig     `json:"retry"`
	Benchmark        BenchmarkConfig `json:"benchmark"`
}

type ModelConfig struct {
	ID                       string   `json:"id"`
	Endpoint                 string   `json:"endpoint"`
	ReasoningBudget          int      `json:"reasoning_budget"`
	ReasoningBudgetParameter string   `json:"reasoning_budget_parameter,omitempty"`
	MaxTokens                int      `json:"max_tokens"`
	Temperature              float64  `json:"temperature"`
	TopP                     *float64 `json:"top_p,omitempty"`
	ReasoningEffort          string   `json:"reasoning_effort,omitempty"`
}

type ProposalConfig struct {
	ProposerCount int `json:"proposer_count"`
	BeamWidth     int `json:"beam_width"`
	TimeoutMS     int `json:"timeout_ms"`
}

type RetryConfig struct {
	MaxAttempts int `json:"max_attempts"`
	BaseDelayMS int `json:"base_delay_ms"`
	MaxDelayMS  int `json:"max_delay_ms"`
}

type BenchmarkConfig struct {
	Seed          int64  `json:"seed"`
	MaxTurns      int    `json:"max_turns"`
	TaskTimeoutMS int    `json:"task_timeout_ms"`
	Transport     string `json:"transport"`
}

func LoadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Manifest{}, errors.New("decode manifest: trailing JSON value")
		}
		return Manifest{}, fmt.Errorf("decode manifest trailing content: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != "0.1.0" || m.BenchmarkVersion != "0.1.0" {
		return errors.New("manifest schema_version and benchmark_version must be 0.1.0")
	}
	if strings.TrimSpace(m.Model.ID) == "" || strings.TrimSpace(m.Model.Endpoint) == "" {
		return errors.New("manifest model id and endpoint are required")
	}
	if m.Model.ReasoningBudget < 0 || m.Model.MaxTokens <= 0 || m.Model.MaxTokens <= m.Model.ReasoningBudget {
		return errors.New("manifest max_tokens must be greater than the non-negative reasoning_budget")
	}
	if parameter := m.Model.ReasoningBudgetParameter; parameter != "" && parameter != "thinking_token_budget" && parameter != "reasoning_budget" {
		return errors.New("manifest reasoning_budget_parameter must be thinking_token_budget or reasoning_budget")
	}
	if m.Model.Temperature < 0 || m.Model.Temperature > 2 {
		return errors.New("manifest temperature must be between 0 and 2")
	}
	if m.Model.TopP != nil && (*m.Model.TopP < 0 || *m.Model.TopP > 1) {
		return errors.New("manifest top_p must be between 0 and 1")
	}
	if effort := m.Model.ReasoningEffort; effort != "" && effort != "none" && effort != "medium" && effort != "high" {
		return errors.New("manifest reasoning_effort must be none, medium, or high")
	}
	if m.Proposal.ProposerCount < 1 || m.Proposal.ProposerCount > 16 ||
		m.Proposal.BeamWidth < 1 || m.Proposal.BeamWidth > 16 || m.Proposal.TimeoutMS <= 0 {
		return errors.New("manifest proposal counts must be between 1 and 16 with a positive timeout_ms")
	}
	if m.Retry.MaxAttempts <= 0 || m.Retry.BaseDelayMS < 0 || m.Retry.MaxDelayMS < m.Retry.BaseDelayMS {
		return errors.New("manifest retry configuration is invalid")
	}
	if m.Benchmark.Seed < 0 || m.Benchmark.MaxTurns <= 0 || m.Benchmark.TaskTimeoutMS <= 0 {
		return errors.New("manifest benchmark seed, max_turns, or task_timeout_ms is invalid")
	}
	if m.Benchmark.Transport != "in_process" {
		return errors.New("static MVB transport must be in_process")
	}
	return nil
}

func (m ModelConfig) BudgetParameter() string {
	if m.ReasoningBudgetParameter == "" {
		return "thinking_token_budget"
	}
	return m.ReasoningBudgetParameter
}

func (p ProposalConfig) Timeout() time.Duration {
	return time.Duration(p.TimeoutMS) * time.Millisecond
}

func (r RetryConfig) BaseDelay() time.Duration {
	return time.Duration(r.BaseDelayMS) * time.Millisecond
}

func (r RetryConfig) MaxDelay() time.Duration {
	return time.Duration(r.MaxDelayMS) * time.Millisecond
}
