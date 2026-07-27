package harness

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"bouncer/internal/nimclient"
)

type proposerFunc func(context.Context, nimclient.ProposalRequest) (nimclient.ProposalResult, error)

func (function proposerFunc) Propose(
	ctx context.Context,
	request nimclient.ProposalRequest,
) (nimclient.ProposalResult, error) {
	return function(ctx, request)
}

type barrierProposer struct {
	mutex   sync.Mutex
	started []nimclient.ProposalRequest
	barrier chan struct{}
	release sync.Once
	failID  string
}

func (p *barrierProposer) Propose(ctx context.Context, request nimclient.ProposalRequest) (nimclient.ProposalResult, error) {
	p.mutex.Lock()
	p.started = append(p.started, request)
	if len(p.started) == 3 {
		p.release.Do(func() { close(p.barrier) })
	}
	p.mutex.Unlock()
	select {
	case <-ctx.Done():
		return nimclient.ProposalResult{}, ctx.Err()
	case <-p.barrier:
	}
	if request.ProposerID == p.failID {
		return nimclient.ProposalResult{}, errors.New("planned failure")
	}
	return nimclient.ProposalResult{ProposerID: request.ProposerID, FinishReason: "stop"}, nil
}

func TestCoordinatorRunsThreeProposersConcurrentlyAndOrdersResults(t *testing.T) {
	proposer := &barrierProposer{barrier: make(chan struct{})}
	coordinator := Coordinator{Proposer: proposer, ProposerCount: 3, Timeout: time.Second}
	results, err := coordinator.ProposeAll(context.Background(), Request{
		TaskID:      "task-001",
		Instruction: "test",
		State:       json.RawMessage(`{}`),
		BaseSeed:    100,
	})
	if err != nil {
		t.Fatalf("ProposeAll returned error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	for index, result := range results {
		want := "agent-" + string(rune('1'+index))
		if result.ProposerID != want {
			t.Fatalf("result %d proposer=%s, want %s", index, result.ProposerID, want)
		}
	}
	proposer.mutex.Lock()
	defer proposer.mutex.Unlock()
	seeds := map[int64]bool{}
	for _, request := range proposer.started {
		seeds[request.Seed] = true
	}
	for _, seed := range []int64{100, 101, 102} {
		if !seeds[seed] {
			t.Fatalf("seed %d was not dispatched", seed)
		}
	}
}

func TestCoordinatorPropagatesProposerFailure(t *testing.T) {
	proposer := &barrierProposer{barrier: make(chan struct{}), failID: "agent-2"}
	coordinator := Coordinator{Proposer: proposer, ProposerCount: 3, Timeout: time.Second}
	_, err := coordinator.ProposeAll(context.Background(), Request{
		TaskID:      "task-001",
		Instruction: "test",
		State:       json.RawMessage(`{}`),
	})
	if err == nil || !stringsContains(err.Error(), "agent-2") {
		t.Fatalf("got error %v, want agent-2 failure", err)
	}
}

type blockingProposer struct{}

func (blockingProposer) Propose(ctx context.Context, _ nimclient.ProposalRequest) (nimclient.ProposalResult, error) {
	<-ctx.Done()
	return nimclient.ProposalResult{}, ctx.Err()
}

func TestCoordinatorEnforcesRoundTimeout(t *testing.T) {
	coordinator := Coordinator{Proposer: blockingProposer{}, ProposerCount: 3, Timeout: 10 * time.Millisecond}
	_, err := coordinator.ProposeAll(context.Background(), Request{
		TaskID:      "task-001",
		Instruction: "test",
		State:       json.RawMessage(`{}`),
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got error %v, want context deadline exceeded", err)
	}
}

func TestCoordinatorProposeRangeUsesStableIdentitiesAndSeeds(t *testing.T) {
	var mutex sync.Mutex
	requests := make([]nimclient.ProposalRequest, 0, 2)
	proposer := proposerFunc(func(_ context.Context, request nimclient.ProposalRequest) (nimclient.ProposalResult, error) {
		mutex.Lock()
		requests = append(requests, request)
		mutex.Unlock()
		return nimclient.ProposalResult{ProposerID: request.ProposerID, FinishReason: "stop"}, nil
	})
	coordinator := Coordinator{Proposer: proposer, ProposerCount: 4, Timeout: time.Second}
	results, err := coordinator.ProposeRange(context.Background(), Request{BaseSeed: 100}, 1, 2)
	if err != nil {
		t.Fatalf("ProposeRange returned error: %v", err)
	}
	if len(results) != 2 || results[0].ProposerID != "agent-2" || results[1].ProposerID != "agent-3" {
		t.Fatalf("unexpected ordered results: %+v", results)
	}
	mutex.Lock()
	defer mutex.Unlock()
	seeds := map[string]int64{}
	for _, request := range requests {
		seeds[request.ProposerID] = request.Seed
	}
	if seeds["agent-2"] != 101 || seeds["agent-3"] != 102 {
		t.Fatalf("unexpected seeds: %v", seeds)
	}
}

func TestCoordinatorRejectsInvalidRange(t *testing.T) {
	coordinator := Coordinator{
		Proposer: proposerFunc(func(_ context.Context, request nimclient.ProposalRequest) (nimclient.ProposalResult, error) {
			return nimclient.ProposalResult{ProposerID: request.ProposerID}, nil
		}),
		ProposerCount: 3,
		Timeout:       time.Second,
	}
	for _, test := range []struct {
		start int
		count int
	}{{-1, 1}, {0, 0}, {2, 2}} {
		if _, err := coordinator.ProposeRange(context.Background(), Request{}, test.start, test.count); err == nil {
			t.Fatalf("range [%d, %d) returned nil error", test.start, test.start+test.count)
		}
	}
}

func stringsContains(value, substring string) bool {
	for index := 0; index+len(substring) <= len(value); index++ {
		if value[index:index+len(substring)] == substring {
			return true
		}
	}
	return false
}
