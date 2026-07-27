//go:build !linux

package executor

import (
	"context"
	"testing"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
)

func TestRootedFailsClosedOnUnsupportedPlatform(t *testing.T) {
	rooted, err := NewRooted(RootedConfig{Root: "/workspace"})
	if err == nil || rooted != nil {
		t.Fatalf("NewRooted returned rooted=%v error=%v", rooted, err)
	}
	var unsupported *Rooted
	if err := unsupported.Close(); err != nil {
		t.Fatalf("Close returned %v", err)
	}
	if _, err := unsupported.Execute(
		context.Background(),
		&benchmark.State{},
		benchmark.Policy{},
		action.Candidate{},
	); err == nil {
		t.Fatal("unsupported rooted executor accepted execution")
	}
}
