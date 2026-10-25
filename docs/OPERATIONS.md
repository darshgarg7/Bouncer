# Operations, observability & recovery

This document provides operational guidelines for running Bouncer, managing ML decision artifacts, monitoring telemetry, executing recovery protocols, and performing release checklists.

---

## 1. Environment & Sandbox Deployment

### Local Process Deployment
Run `bouncer-run` with explicit manifest and task configurations:
```bash
bin/bouncer-run \
  -endpoint http://127.0.0.1:8001/v1 \
  -task benchmarks/tasks/task-001.json \
  -event-log benchmarks/results/events.jsonl \
  -output benchmarks/results/result.json
```

### Reference Sandbox Server
The standalone reference sandbox server ([server.go](../internal/sandbox/server.go)) exposes HTTP endpoints for remote execution with idempotency key locking.

---

## 2. ML Artifact Operations & Lifecycle

Bouncer uses three types of pre-loaded ML and statistical decision artifacts:
1. **Objective Calibration:** Transforms model-authored estimates into calibrated routing objectives ([calibration.go](../internal/calibration/calibration.go)).
2. **Learned Routing:** Predicts progress, terminal success, cost, and risk for 5-objective Pareto selection ([learned.go](../internal/router/learned.go)).
3. **Isolation Forest Anomaly Detection:** Computes post-execution anomaly scores on rolling telemetry ([scorer.go](../internal/anomaly/scorer.go)).

### Immutable Artifact Gates
- Every artifact MUST specify `schema_version`, provenance metadata, and SHA-256 digests.
- **Active Mode Gate:** Anomaly detection defaults to `shadow` mode. Active mode requires qualifying held-out validation evidence (TPR $\ge 80\%$, FPR $\le 5\%$, non-overlapping identities). Hand-authored artifacts are refused in active mode.

---

## 3. Observability & Telemetry

Bouncer emits rolling telemetry features and deterministic alerts ([rules.go](../internal/monitoring/rules.go)):
- **Rejection Rate:** Ratio of policy-rejected proposals to total proposals in the rolling window.
- **Retry Rate:** Ratio of transient provider retries.
- **No-Progress Streak:** Consecutive turns without state mutation or validation progress.
- **Tool Switch Rate:** Frequency of operation class switching.
- **Latency Delta:** Moving average of turn latency.

### Logging & Tamper Evidence
All events are appended to a line-delimited JSON log ([jsonl.go](../internal/eventlog/jsonl.go)). Each event contains:
- `event_id`, `run_id`, `task_id`, `seq_num`
- `prev_event_sha256` (SHA-256 of previous line)
- `event_sha256` (SHA-256 of current canonical payload)

Verify event log integrity with:
```bash
bin/bouncer-verify-log -event-log benchmarks/results/events.jsonl
```

---

## 4. Recovery & Reconciliation

### Indeterminate Execution (`409 Indeterminate`)
When a mutation request is claimed before backend execution, but the process fails before writing the response:
1. The reference server leaves the idempotency key **`409 Indeterminate`**.
2. **Never delete a claim file blindly to retry.**
3. Quarantine the worker and key.
4. Inspect the target system via an independent read path to verify if the state delta occurred.
5. Manually reconcile the record and issue a fresh external anchor.

### Backup and Restore
1. Stop execution traffic and complete server graceful shutdown.
2. Snapshot the idempotency storage directory and executor workspace as one unit.
3. Backup event logs and external terminal hash anchors.
4. Restore into a clean directory and execute strict JSON validation before enabling traffic.

---

## 5. Release Checklist

Run the full publication release audit gate before tagging a release:

```bash
make release-check
```

This gate verifies:
- Python test suite (51 tests);
- Go race detector and static analysis (`go vet`, Ruff, strict `mypy`);
- Coverage ratchets (83%+ overall Go, 93%+ policy, 94%+ executor);
- Differential Go/Python policy parity (100,000 cases);
- Credential scanning (NVIDIA, OpenAI, AWS, private keys);
- Documentation link integrity;
- Publication claims matching `benchmarks/publication-claims.json`;
- Pilot lifecycle summaries and external final-hash anchors.
