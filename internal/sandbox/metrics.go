package sandbox

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type Metrics struct {
	requests        atomic.Uint64
	executions      atomic.Uint64
	replays         atomic.Uint64
	errors          atomic.Uint64
	durationNanos   atomic.Uint64
	durationSamples atomic.Uint64
}

func NewMetrics() *Metrics { return &Metrics{} }

func (m *Metrics) begin() func() {
	m.requests.Add(1)
	started := time.Now()
	return func() {
		m.durationNanos.Add(uint64(time.Since(started)))
		m.durationSamples.Add(1)
	}
}

func (m *Metrics) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = fmt.Fprintf(
		writer,
		"# HELP bouncer_sandbox_requests_total Execution requests received.\n"+
			"# TYPE bouncer_sandbox_requests_total counter\n"+
			"bouncer_sandbox_requests_total %d\n"+
			"# HELP bouncer_sandbox_executions_total Backend executions performed.\n"+
			"# TYPE bouncer_sandbox_executions_total counter\n"+
			"bouncer_sandbox_executions_total %d\n"+
			"# HELP bouncer_sandbox_idempotency_replays_total Cached responses replayed.\n"+
			"# TYPE bouncer_sandbox_idempotency_replays_total counter\n"+
			"bouncer_sandbox_idempotency_replays_total %d\n"+
			"# HELP bouncer_sandbox_errors_total Requests ending in an error.\n"+
			"# TYPE bouncer_sandbox_errors_total counter\n"+
			"bouncer_sandbox_errors_total %d\n"+
			"# HELP bouncer_sandbox_request_duration_seconds Request duration.\n"+
			"# TYPE bouncer_sandbox_request_duration_seconds summary\n"+
			"bouncer_sandbox_request_duration_seconds_sum %.9f\n"+
			"bouncer_sandbox_request_duration_seconds_count %d\n",
		m.requests.Load(),
		m.executions.Load(),
		m.replays.Load(),
		m.errors.Load(),
		float64(m.durationNanos.Load())/float64(time.Second),
		m.durationSamples.Load(),
	)
}
