package eventlog

import (
	"context"
	"errors"

	"bouncer/internal/control"
)

type TraceSink struct {
	Writer *Writer
	RunID  string
	TaskID string
	Seed   int64
}

func (s TraceSink) Append(ctx context.Context, trace control.TraceEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.Writer == nil {
		return errors.New("trace sink writer is required")
	}
	return s.Writer.Append(Event{
		EventType: trace.EventType,
		RunID:     s.RunID,
		TaskID:    s.TaskID,
		StepID:    trace.StepID,
		Seed:      s.Seed,
		Payload:   trace.Payload,
	})
}
