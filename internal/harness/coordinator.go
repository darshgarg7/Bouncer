package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"bouncer/internal/nimclient"
)

type Proposer interface {
	Propose(context.Context, nimclient.ProposalRequest) (nimclient.ProposalResult, error)
}

type Coordinator struct {
	Proposer      Proposer
	ProposerCount int
	Timeout       time.Duration
}

type Request struct {
	TaskID      string
	Instruction string
	State       json.RawMessage
	Policy      json.RawMessage
	BaseSeed    int64
}

type indexedResult struct {
	index  int
	result nimclient.ProposalResult
	err    error
}

func (c Coordinator) ProposeAll(ctx context.Context, request Request) ([]nimclient.ProposalResult, error) {
	return c.ProposeRange(ctx, request, 0, c.ProposerCount)
}

// ProposeRange requests a stable subset of proposer identities. It enables the
// control loop to begin with a small compute budget and expand only when the
// first proposal set is invalid or lacks objective-space diversity.
func (c Coordinator) ProposeRange(
	ctx context.Context,
	request Request,
	startIndex int,
	count int,
) ([]nimclient.ProposalResult, error) {
	if c.Proposer == nil {
		return nil, errors.New("coordinator proposer is required")
	}
	if startIndex < 0 || count < 1 || startIndex+count > c.ProposerCount || c.ProposerCount > 16 {
		return nil, fmt.Errorf(
			"coordinator proposer range [%d, %d) is outside configured count %d",
			startIndex,
			startIndex+count,
			c.ProposerCount,
		)
	}
	if c.Timeout <= 0 {
		return nil, errors.New("coordinator timeout must be positive")
	}

	runContext, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	resultChannel := make(chan indexedResult, count)
	for offset := 0; offset < count; offset++ {
		index := startIndex + offset
		go func(index int) {
			proposerID := fmt.Sprintf("agent-%d", index+1)
			result, err := c.Proposer.Propose(runContext, nimclient.ProposalRequest{
				TaskID:      request.TaskID,
				Instruction: request.Instruction,
				State:       request.State,
				Policy:      request.Policy,
				ProposerID:  proposerID,
				Seed:        request.BaseSeed + int64(index),
			})
			resultChannel <- indexedResult{index: index, result: result, err: err}
		}(index)
	}

	results := make([]indexedResult, 0, count)
	var firstErr error
	for range count {
		select {
		case <-runContext.Done():
			if firstErr != nil {
				return nil, firstErr
			}
			return nil, fmt.Errorf("proposal round failed: %w", runContext.Err())
		case item := <-resultChannel:
			results = append(results, item)
			if item.err != nil && firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", fmt.Sprintf("agent-%d", item.index+1), item.err)
				cancel()
			}
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	sort.Slice(results, func(i, j int) bool { return results[i].index < results[j].index })
	ordered := make([]nimclient.ProposalResult, len(results))
	for i := range results {
		ordered[i] = results[i].result
	}
	return ordered, nil
}
