# Development guide & package architecture

This guide provides the quickest route from a fresh checkout to understanding the codebase, running local tasks, testing control boundaries, and contributing safely.

---

## Mental Model

Bouncer consists of seven named stages:

1. **Proposal:** A provider proposes typed candidate actions.
2. **Admission:** The Go policy evaluator rejects candidates violating declared constraints.
3. **Calibration:** A trusted artifact converts raw estimates into routing objectives.
4. **Routing:** The router selects one admitted candidate using a named strategy.
5. **Execution:** An executor applies the action to virtual or remote state.
6. **Monitoring & Verification:** Telemetry observes outcomes; executor state diffs are verified.
7. **Recording:** The event log records the run and verifies hash-chained integrity.

The model participates **only** in step 1. It never approves its own action.

---

## Package Map & Dependency Direction

"Ownership" here means architectural responsibility. Lower boundaries must never import their callers (e.g., policy cannot import routing or learning; executors cannot import control):

| Package | Owns | May depend on |
| :--- | :--- | :--- |
| `internal/action` | Provider-neutral candidate contract | Standard library only |
| `internal/benchmark` | Typed tasks and virtual state | Standard library only |
| `internal/policy` | Canonical authorization | Action and benchmark contracts |
| `internal/calibration` | Trusted routing-objective derivation | Action contract |
| `internal/anomaly` | Isolation Forest artifacts & scoring | Monitoring feature contract |
| `internal/learning` | Portable outcome inference | Admitted action & state contracts |
| `internal/router` | Baseline and learned selection | Action, calibration, learning |
| `internal/executor` | State mutation & transition validation | Action and benchmark contracts |
| `internal/eventlog` | Lifecycle and evidence integrity | Event contract only |
| `internal/control` | Stage orchestration | All runtime boundaries above |
| `cmd/*` | Configuration and dependency wiring | Public internal package APIs |
| `benchmarking/*` | Offline studies & artifact production | Published contracts; never runtime |

Enforce architecture boundaries with:
```bash
make architecture-check
```

---

## Running the Demo

The primary live demo tests 5 control boundaries with zero credentials, network access, or Docker:

```bash
make demo
```

It exercises:
1. Strict malformed-proposal rejection;
2. Deterministic policy rejection of an out-of-root write;
3. Successful virtual execution of an admitted write;
4. Event-chain tamper detection; and
5. Learned-routing shadow mode.

**Multi-Process Docker Demo:**
```bash
docker compose -f compose.demo.yaml up --build --abort-on-container-exit --exit-code-from bouncer
```

---

## Code Map: Following a Request

| Stage | Location | Key Code |
| :--- | :--- | :--- |
| CLI Assembly | `cmd/bouncer-run/main.go` | Manifest overrides and dependency wiring |
| Provider Call | `internal/nimclient/client.go` | Request construction, strict beam decoding |
| Coordinator | `internal/harness/coordinator.go` | Stable proposer IDs, seeds, deadline ordering |
| Control Loop | `internal/control/loop.go` | Turn lifecycle and failure propagation |
| Policy Evaluator | `internal/policy/evaluator.go` | Operations, path bounds, dependencies, quotas |
| Objective Calibration | `internal/calibration/calibration.go` | Bounds, transforms, operation priors |
| Router | `internal/router/router.go` | Risk ceiling, ranking, strategy selection |
| Executor | `internal/executor/virtual.go` | Canonical state transitions |
| Remote Executor | `internal/executor/remote.go` | Idempotency key & transition validation |
| Event Log | `internal/eventlog/jsonl.go` | Hash-chained event log verification |

---

## Useful Development Commands

```bash
make bootstrap             # Environment setup
make test                  # Go race detector & Python tests
make lint                  # go vet, Ruff, strict mypy
make coverage              # Current coverage ratchets
make verify-policy-parity  # Go/Python 100,000-case differential gate
make fuzz-smoke            # Bounded trust-boundary fuzzing
make release-check         # Complete release audit gate
```

---

## Documentation Map

- [System Design Walkthrough](SYSTEM_DESIGN_WALKTHROUGH.md): Runtime topology and state-machine walkthrough.
- [Threat Model](THREAT_MODEL.md): Assets, attackers, controls, and residual risk.
- [Protocol](PROTOCOL.md): Typed request, response, event, and idempotency contracts.
- [Claims & Evidence](CLAIMS.md): Allowed wording and supporting artifacts.
- [Benchmarking](BENCHMARKING.md): Controlled studies and evaluation methods.
- [Operations](OPERATIONS.md): Deployments, ML artifacts, observability, and recovery.
- [Production Readiness](PRODUCTION_READINESS.md): Implemented versus unqualified controls.
- [Project History](PROJECT_HISTORY.md): Authorship, failed approaches, and design history.
- [Hiring Guide](HIRING_GUIDE.md): Interview walkthrough and candidate brief.
