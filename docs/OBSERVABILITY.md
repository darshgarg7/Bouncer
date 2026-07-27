# Observability and operational gates

Bouncer emits two complementary artifacts:

- the final result JSON contains aggregate metrics, final state, oracle failures, and the complete in-memory trace;
- `-event-log` writes each trace event durably as JSONL while the run is active.

The event log starts with `run.started` and ends with `run.completed` or `run.failed`. Files are created with exclusive-create semantics, so a new run cannot silently overwrite previous evidence. Event schema `0.2.0` adds a monotonic sequence and SHA-256 chain; verify it with `bouncer-verify-log` before analysis. The verifier also enforces a single run/task identity and rejects suffix-truncated logs. Preserve the returned final hash outside the log and pass it back with `-expected-final-hash` when an external anchor is available.

`bouncer-run` and `bouncer-sandbox` accept `-otlp-endpoint` and `-trace-sample-ratio`. Proposal, projection, routing, and execution spans propagate W3C trace context through provider and sandbox HTTP calls. Decision events include trace and span IDs, but prompt and state content are not added as span attributes. The sandbox exposes Prometheus text metrics at `/metrics` for requests, executions, idempotency replays, errors, and aggregate duration.

## Required dashboards

| Signal | Dimensions | Alert condition |
| --- | --- | --- |
| Proposal completion | model, endpoint, finish reason | Any sustained non-`stop` rate |
| Truncation | model, manifest version | Greater than 1% |
| Retry load | HTTP status, attempt | Rate-limit or 5xx spike |
| Token use | condition, task, model | Budget or baseline regression |
| Constraint decisions | code, operation, task | New or rapidly increasing code |
| Execution | backend, operation, outcome | Any unauthorized or unattributed mutation |
| Task outcome | task class, policy version | Pass-rate non-inferiority breach |
| Latency | proposal, projection, execution, end-to-end | Frozen p95 SLO breach |

## Event handling

Production collectors should tail JSONL into an append-only store and add host, deployment, and trace identifiers outside the signed event payload. Prompt and state fields can contain sensitive data; redact or encrypt them before leaving the execution boundary.

Do not sample:

- constraint violations;
- selected actions;
- execution state diffs;
- failed runs; or
- human approval decisions.

## Initial service objectives

- 100% of selected actions have a preceding constraint decision.
- 100% of mutations have an idempotency key and observed state diff.
- Zero accepted `finish_reason: length` responses.
- Zero remote sandbox calls over plaintext outside explicit local-test mode.
- At least 99% of runs end in a terminal `run.completed` or `run.failed` event.

These are initial operating gates, not claims of production reliability.
