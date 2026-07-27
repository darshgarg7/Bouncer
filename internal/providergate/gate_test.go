package providergate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"bouncer/internal/harness"
	"bouncer/internal/nimclient"
)

type proposerFunc func(context.Context, nimclient.ProposalRequest) (nimclient.ProposalResult, error)

func (function proposerFunc) Propose(ctx context.Context, request nimclient.ProposalRequest) (nimclient.ProposalResult, error) {
	return function(ctx, request)
}

func testCoordinator(proposer harness.Proposer) harness.Coordinator {
	return harness.Coordinator{Proposer: proposer, ProposerCount: 3, Timeout: time.Second}
}

func TestRunnerRecordsSuccessfulBatchesAndUsage(t *testing.T) {
	proposer := proposerFunc(func(_ context.Context, request nimclient.ProposalRequest) (nimclient.ProposalResult, error) {
		return nimclient.ProposalResult{
			ProposerID:   request.ProposerID,
			FinishReason: "stop",
			Usage: nimclient.Usage{
				PromptTokens:     10,
				CompletionTokens: 20,
				ReasoningTokens:  5,
				TotalTokens:      30,
			},
		}, nil
	})
	var raw bytes.Buffer
	summary, err := (Runner{Coordinator: testCoordinator(proposer), Batches: 2}).Run(
		context.Background(),
		harness.Request{TaskID: "task", Instruction: "test", State: json.RawMessage(`{}`), BaseSeed: 10},
		&raw,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Passed || summary.CompletedBatches != 2 || summary.SuccessfulProposalCalls != 6 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.TotalTokens != 180 || summary.FinishReasons["stop"] != 6 {
		t.Fatalf("unexpected telemetry: %+v", summary)
	}
	if lines := strings.Count(strings.TrimSpace(raw.String()), "\n") + 1; lines != 2 {
		t.Fatalf("got %d JSONL records", lines)
	}
}

func TestRunnerClassifiesTruncationAndContinues(t *testing.T) {
	proposer := proposerFunc(func(_ context.Context, request nimclient.ProposalRequest) (nimclient.ProposalResult, error) {
		if request.ProposerID == "agent-2" {
			return nimclient.ProposalResult{}, nimclient.ErrTruncated
		}
		return nimclient.ProposalResult{ProposerID: request.ProposerID, FinishReason: "stop"}, nil
	})
	var raw bytes.Buffer
	summary, err := (Runner{Coordinator: testCoordinator(proposer), Batches: 2}).Run(
		context.Background(),
		harness.Request{TaskID: "task", Instruction: "test", State: json.RawMessage(`{}`)},
		&raw,
	)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Passed || summary.FailedBatches != 2 || summary.ErrorClasses["finish_reason_length"] != 2 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestRunnerValidatesInputs(t *testing.T) {
	_, err := (Runner{}).Run(context.Background(), harness.Request{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "batches") {
		t.Fatalf("got error %v", err)
	}
	_, err = (Runner{Batches: 1}).Run(context.Background(), harness.Request{}, nil)
	if err == nil || !strings.Contains(err.Error(), "output") {
		t.Fatalf("got error %v", err)
	}
}

func TestClassifyHTTPAndContextErrors(t *testing.T) {
	if got := classifyError(&nimclient.HTTPStatusError{StatusCode: 429}); got != "http_429" {
		t.Fatalf("got %q", got)
	}
	if got := classifyError(context.DeadlineExceeded); got != "deadline_exceeded" {
		t.Fatalf("got %q", got)
	}
	if got := classifyError(errors.New("other")); got != "proposal_or_validation" {
		t.Fatalf("got %q", got)
	}
}
