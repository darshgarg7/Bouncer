# Production-readiness checklist

Bouncer is not production qualified. This checklist separates implemented
controls from evidence still required before stronger deployment claims.

## Implemented and locally exercised

- [x] Strict proposal, manifest, task, artifact, and event decoding.
- [x] Canonical deterministic authorization with an independent Python oracle.
- [x] Explicit read-deny and mutation-protected paths in both policy engines.
- [x] Rooted Linux file lookup with beneath/no-symlink resolution and hard-link denial.
- [x] Authenticated remote execution protocol and state-transition validation.
- [x] Durable idempotency records with fail-closed indeterminate states.
- [x] Complete run lifecycle and externally anchorable SHA-256 event chains.
- [x] Race-enabled tests, coverage ratchets, source-bound evidence, and secret scanning.
- [x] Request-size limits, process-wide rate limiting, HTTP timeouts, and graceful shutdown hooks.
- [x] Non-root, read-only-root Kubernetes reference configuration targeting gVisor.

## Security qualification still required

- [ ] Symlink, hard-link, rename, mount, descriptor, and concurrent-replacement campaign.
- [ ] Real gVisor escape/containment qualification on the release runtime.
- [ ] Workload identity or mutual TLS for remote execution.
- [ ] Enforced egress policy and request/time/rate limits qualified under hostile load.
- [ ] `govulncheck`, CodeQL, dependency audit, and container scanning in hosted CI.
- [ ] Release SBOM, signatures, and verifiable build provenance.
- [ ] Independent security review and tracked remediation.

## Reliability qualification still required

- [ ] Load and soak tests with defined SLOs.
- [ ] Process-crash, disk-full, and storage-corruption exercises.
- [ ] Retry-storm and provider-degradation tests.
- [ ] Graceful shutdown verified with in-flight mutating requests and forced-deadline expiry.
- [ ] Backup, restore, and indeterminate-key reconciliation drills.
- [x] Example Grafana dashboard and initial alert/SLO recommendations are checked in.
- [ ] Dashboard and alert thresholds validated against load and incident scenarios.

## Evidence qualification still required

- [ ] Recognized agent-security benchmark.
- [ ] Realistic task benchmark with equal-permission baselines.
- [ ] Multiple model families, multiple seeds, and uncertainty intervals.
- [ ] Pre-registered analysis separating safety from cost and latency effects.
- [ ] Fresh-clone reproduction by someone outside the project.
- [ ] Independent technical or academic review.

## Release decision

Until every applicable item is closed, describe Bouncer as a research prototype
or reference implementation. Do not claim production safety, general sandbox
containment, prompt-injection detection, or learned-routing effectiveness.
