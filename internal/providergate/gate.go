package providergate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"time"

	"bouncer/internal/harness"
	"bouncer/internal/nimclient"
)

type Runner struct {
	Coordinator harness.Coordinator
	Batches     int
}

type BatchRecord struct {
	BatchIndex int                        `json:"batch_index"`
	BaseSeed   int64                      `json:"base_seed"`
	Success    bool                       `json:"success"`
	DurationMS int64                      `json:"duration_ms"`
	Proposals  []nimclient.ProposalResult `json:"proposals,omitempty"`
	ErrorClass string                     `json:"error_class,omitempty"`
	Error      string                     `json:"error,omitempty"`
}

type Summary struct {
	SchemaVersion           string         `json:"schema_version"`
	StartedAt               time.Time      `json:"started_at"`
	CompletedAt             time.Time      `json:"completed_at"`
	RequestedBatches        int            `json:"requested_batches"`
	CompletedBatches        int            `json:"completed_batches"`
	FailedBatches           int            `json:"failed_batches"`
	ExpectedProposalCalls   int            `json:"expected_proposal_calls"`
	SuccessfulProposalCalls int            `json:"successful_proposal_calls"`
	FinishReasons           map[string]int `json:"finish_reasons"`
	ErrorClasses            map[string]int `json:"error_classes"`
	PromptTokens            int            `json:"prompt_tokens"`
	CompletionTokens        int            `json:"completion_tokens"`
	ReasoningTokens         int            `json:"reasoning_tokens"`
	TotalTokens             int            `json:"total_tokens"`
	P50BatchLatencyMS       int64          `json:"p50_batch_latency_ms"`
	P95BatchLatencyMS       int64          `json:"p95_batch_latency_ms"`
	Passed                  bool           `json:"passed"`
	ModelID                 string         `json:"model_id,omitempty"`
	Endpoint                string         `json:"endpoint,omitempty"`
	BudgetParameter         string         `json:"budget_parameter,omitempty"`
	ManifestSHA256          string         `json:"manifest_sha256,omitempty"`
	TaskSHA256              string         `json:"task_sha256,omitempty"`
	RawArtifact             string         `json:"raw_artifact,omitempty"`
}

func (r Runner) Run(ctx context.Context, request harness.Request, output io.Writer) (Summary, error) {
	if r.Batches <= 0 {
		return Summary{}, errors.New("provider gate batches must be positive")
	}
	if output == nil {
		return Summary{}, errors.New("provider gate output is required")
	}
	started := time.Now().UTC()
	summary := Summary{
		SchemaVersion:         "0.1.0",
		StartedAt:             started,
		RequestedBatches:      r.Batches,
		ExpectedProposalCalls: r.Batches * r.Coordinator.ProposerCount,
		FinishReasons:         map[string]int{},
		ErrorClasses:          map[string]int{},
	}
	latencies := make([]int64, 0, r.Batches)
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)

	for batch := 0; batch < r.Batches; batch++ {
		if err := ctx.Err(); err != nil {
			summary.ErrorClasses[classifyError(err)]++
			break
		}
		baseSeed := request.BaseSeed + int64(batch*r.Coordinator.ProposerCount)
		batchRequest := request
		batchRequest.BaseSeed = baseSeed
		batchStarted := time.Now()
		proposals, err := r.Coordinator.ProposeAll(ctx, batchRequest)
		duration := time.Since(batchStarted).Milliseconds()
		latencies = append(latencies, duration)
		record := BatchRecord{
			BatchIndex: batch,
			BaseSeed:   baseSeed,
			Success:    err == nil,
			DurationMS: duration,
			Proposals:  proposals,
		}
		if err != nil {
			record.ErrorClass = classifyError(err)
			record.Error = err.Error()
			summary.FailedBatches++
			summary.ErrorClasses[record.ErrorClass]++
		} else {
			summary.CompletedBatches++
			summary.SuccessfulProposalCalls += len(proposals)
			for _, proposal := range proposals {
				summary.FinishReasons[proposal.FinishReason]++
				summary.PromptTokens += proposal.Usage.PromptTokens
				summary.CompletionTokens += proposal.Usage.CompletionTokens
				summary.ReasoningTokens += proposal.Usage.ReasoningTokens
				summary.TotalTokens += proposal.Usage.TotalTokens
			}
		}
		if err := encoder.Encode(record); err != nil {
			return Summary{}, fmt.Errorf("encode provider gate batch %d: %w", batch, err)
		}
	}

	summary.CompletedAt = time.Now().UTC()
	summary.P50BatchLatencyMS = percentile(latencies, 0.50)
	summary.P95BatchLatencyMS = percentile(latencies, 0.95)
	summary.Passed = summary.CompletedBatches == summary.RequestedBatches &&
		summary.FailedBatches == 0 &&
		summary.SuccessfulProposalCalls == summary.ExpectedProposalCalls &&
		summary.FinishReasons["stop"] == summary.ExpectedProposalCalls
	return summary, nil
}

func classifyError(err error) string {
	if errors.Is(err, nimclient.ErrTruncated) {
		return "finish_reason_length"
	}
	if errors.Is(err, nimclient.ErrUnexpectedFinishReason) {
		return "unexpected_finish_reason"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var statusError *nimclient.HTTPStatusError
	if errors.As(err, &statusError) {
		return "http_" + strconv.Itoa(statusError.StatusCode)
	}
	return "proposal_or_validation"
}

func percentile(values []int64, quantile float64) int64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]int64(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := int(math.Ceil(quantile*float64(len(ordered)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(ordered) {
		index = len(ordered) - 1
	}
	return ordered[index]
}
