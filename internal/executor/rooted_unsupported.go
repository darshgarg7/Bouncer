//go:build !linux

package executor

import (
	"context"
	"errors"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
)

type RootedConfig struct {
	Root         string
	MaxReadBytes int64
}

type Rooted struct{}

func NewRooted(RootedConfig) (*Rooted, error) {
	return nil, errors.New("rooted executor requires Linux openat2")
}

func (*Rooted) Close() error { return nil }

func (*Rooted) Execute(
	context.Context,
	*benchmark.State,
	benchmark.Policy,
	action.Candidate,
) (Outcome, error) {
	return Outcome{}, errors.New("rooted executor requires Linux openat2")
}
