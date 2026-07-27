# ML routing operations

Bouncer's learned router is implemented, but it is not enabled by default. The
runtime has three explicit promotion modes:

| Mode | Model inference | Changes execution | Failure behavior |
| --- | --- | --- | --- |
| `disabled` | No | No | Existing calibrated router remains unchanged |
| `shadow` | Yes | No | Ranking-gate failures are logged; execution uses the existing router |
| `active` | Yes | Yes, inside the policy-admitted set | Invalid artifacts, predictions, uncertainty, or an empty safe frontier fail the run |

The checked-in `configs/learning-artifact.bootstrap.json` is a wiring fixture.
The CLI refuses to activate it because it was not trained or validated on
measured outcomes.

## 1. Collect shadow evidence

Build the binaries and run tasks with a new event-log path for every run:

```bash
make build

bin/bouncer-run \
  -manifest configs/run-manifest.local.json \
  -task benchmarks/tasks/task-001.json \
  -learning-mode shadow \
  -learning-artifact configs/learning-artifact.bootstrap.json \
  -event-log benchmarks/results/ml/task-001-seed-42.jsonl \
  -seed 42

bin/bouncer-verify-log \
  -event-log benchmarks/results/ml/task-001-seed-42.jsonl
```

Shadow events contain:

- the complete policy-admitted candidate set;
- privacy-bounded state and candidate evidence;
- the exact selected-action behavior probability;
- artifact, feature-schema, calibration, and policy identities;
- model predictions and the retained frontier;
- state-before and state-after hashes;
- realized executor latency, deterministic progress, terminal status, and new adverse incidents; and
- explicit label censoring.

Candidate arguments and file contents are not copied into the new learning
records. State hashes bind observations to the executed state without exposing
those contents.

## 2. Build trajectories

Verify each event chain before using it. Then join the decisions, outcomes, and
terminal labels:

```bash
.venv/bin/python -m benchmarking.learning.observations \
  --event-log benchmarks/results/ml/task-001-seed-42.jsonl \
  --event-log benchmarks/results/ml/task-002-seed-42.jsonl \
  --output benchmarks/results/ml/trajectories.jsonl
```

The builder rejects missing decisions, missing outcomes, duplicate runs,
changed identities, non-contiguous sequences, invalid propensities, incomplete
features, and ambiguous censoring. It does not replace the standalone
cryptographic verifier.

## 3. Train a portable artifact

```bash
.venv/bin/python -m benchmarking.learning.outcomes \
  --input benchmarks/results/ml/trajectories.jsonl \
  --artifact benchmarks/results/ml/router-v1.json \
  --report benchmarks/results/ml/router-v1-validation.json \
  --artifact-id router-v1

.venv/bin/python tools/validate_contracts.py \
  --learning-artifact benchmarks/results/ml/router-v1.json
```

Training uses a trajectory-held-out split and produces independent models for
progress, success, latency, cost, and adverse risk. Binary outcomes use
regularized logistic regression; non-negative latency and cost use ridge
regression in `log1p` space. The artifact also contains a Dirichlet-smoothed
first-order transition prior.

The runtime checks every field, feature name, coefficient, link function,
probability, uncertainty, provenance field, and artifact digest before serving
it. Inference is deterministic Go code and has no Python or model-server
dependency.

### Cost is currently censored

The existing executor does not return a trustworthy realized monetary or tool
charge. Runtime logs therefore store `cost_units: null` with
`cost_censored: true`. The trainer will not pretend the calibrated routing
estimate is a measured label. A generic billing interface is intentionally
deferred until a genuinely metered executor can return idempotency-bound
receipts; emitting zero or allocated mock charges would weaken this boundary.

Provider proposal spend is different: it is incurred before selection and may
be shared by a whole beam. A future paid-study ledger may apply a frozen, hashed
price catalog to provider-reported usage and label the result
`calculated_list_price`. It must remain run-level accounting, not an automatic
execution-cost training label. The cost model therefore retains its calibrated
estimate with high uncertainty, and normal active uncertainty gates should
reject it.

## 4. Evaluate sequential value and policy support

Fit vector-valued Q estimates for the fixed logged policy:

```bash
.venv/bin/python -m benchmarking.learning.fitted_q \
  --input benchmarks/results/ml/trajectories.jsonl \
  --output benchmarks/results/ml/fqe-v1.json
```

Run the known-truth simulator and the existing off-policy evaluation gates:

```bash
make evaluate-learning
make evaluate-ope-simulation
```

The simulator is a plumbing and estimator test, not evidence that a learned
production policy is better. Promotion requires held-out real-task results,
adequate action support, stable importance weights, calibrated uncertainty,
and non-inferior pass and adverse-event rates.

## 5. Train the static anomaly model

Deterministic runtime rules already detect rejection bursts, no-progress loops,
mutation-budget exhaustion, and repeated tool alternation. Export the
`monitoring` members of `execution.completed` events as anomaly-window JSONL,
then fit the background Isolation Forest:

```bash
.venv/bin/python -m benchmarking.learning.anomaly \
  --input benchmarks/results/ml/anomaly-windows.jsonl \
  --output benchmarks/results/ml/isolation-forest-v1.json \
  --artifact-id isolation-forest-v1 \
  --threshold 0.65

.venv/bin/python tools/validate_contracts.py \
  --anomaly-artifact benchmarks/results/ml/isolation-forest-v1.json
```

The trainer freezes the feature order, trees, threshold, source-data digest,
seed, and creation time into a portable artifact. It emits a shadow-only
artifact unless a separate labeled holdout is supplied. Start the runtime in
shadow mode:

```bash
bin/bouncer-run \
  -manifest configs/run-manifest.local.json \
  -task benchmarks/tasks/task-001.json \
  -anomaly-mode shadow \
  -anomaly-artifact benchmarks/results/ml/isolation-forest-v1.json \
  -event-log benchmarks/results/ml/anomaly-shadow-task-001-seed-42.jsonl
```

Shadow threshold crossings and scoring errors are recorded without changing
execution. The checked-in bootstrap artifact is only a wiring fixture. Neither
it nor a trained artifact is evidence of prompt-injection detection.

## 6. Qualify and activate the anomaly circuit breaker

Training rows must satisfy `anomaly-window.schema.json`; active eligibility
requires a JSONL holdout satisfying
`anomaly-validation-window.schema.json`, including run/task/turn identity and a
boolean `is_anomaly`. The trainer rejects duplicate identities, declared
identity overlap between training and validation, and identical source digests.
The frozen minimum gate is 20 validation rows, at least five rows per class,
true-positive rate at least 0.80, and false-positive rate at most 0.05:

```bash
.venv/bin/python -m benchmarking.learning.anomaly \
  --input benchmarks/results/ml/anomaly-train-windows.jsonl \
  --validation-input benchmarks/results/ml/anomaly-validation-windows.jsonl \
  --output benchmarks/results/ml/isolation-forest-v1-active.json \
  --artifact-id isolation-forest-v1-active \
  --threshold 0.65 \
  --active-eligible

bin/bouncer-run \
  -manifest configs/run-manifest.local.json \
  -task benchmarks/tasks/task-001.json \
  -anomaly-mode active \
  -anomaly-artifact benchmarks/results/ml/isolation-forest-v1-active.json \
  -event-log benchmarks/results/ml/anomaly-active-task-001-seed-42.jsonl
```

The CLI strictly reloads the artifact and rechecks eligibility before any
provider request. At runtime, ordering is fixed: policy admission, routing,
execution, independent transition verification, feature observation, anomaly
scoring, and then—on a threshold crossing—stopping all subsequent execution.
The triggering action has already executed. An active scoring error is recorded
with that transition and then fails closed before another action.

`active_eligible` is reviewed local provenance, not a signature or independent
certification. Identity and digest checks cannot detect copied rows whose
identities were rewritten, or establish label quality.

Two current feature limitations matter during qualification: `retry_rate` is
zero until attempt-level retry telemetry is connected, and `transition_nll` is
zero unless learned transition scoring is enabled. Do not infer performance
for those signals from the mechanism tests.

## 7. Promote learned routing deliberately

Only switch to `active` after the validation report and OPE gates pass:

```bash
bin/bouncer-run \
  -manifest configs/run-manifest.local.json \
  -task benchmarks/tasks/task-001.json \
  -learning-mode active \
  -learning-artifact benchmarks/results/ml/router-v1.json \
  -learning-risk-ceiling 0.10 \
  -learning-max-relative-uncertainty 0.20 \
  -learning-frontier-limit 16 \
  -event-log benchmarks/results/ml/active-task-001-seed-42.jsonl
```

Active ordering is fixed:

1. deterministic policy admission;
2. trusted feature extraction;
3. independent outcome prediction;
4. uncertainty-adjusted risk and confidence gates;
5. five-objective nondomination;
6. crowding-based frontier cap, if needed;
7. safety-first selection; and
8. verified execution.

Safe epsilon-greedy, conservative LinUCB, and Linear Thompson Sampling are
implemented in `benchmarking.learning.bandits` as offline challengers. They are
not live strategies yet. LinUCB operates on feature vectors rather than
candidate identities. Thompson sampling intentionally reports no production
propensity because its marginal action probability must be estimated and
validated before it can enter OPE-backed deployment.

## Promotion checklist

- [ ] Every source event chain verifies and has an external final-hash anchor.
- [ ] Execution-cost labels remain censored unless a metered executor supplies
  idempotency-bound receipts; provider spend remains a separate run-level label.
- [ ] Train, validation, and test splits are separated by trajectory and time.
- [ ] Outcome calibration beats operation priors on held-out tasks.
- [ ] Markov features show lift in an ablation and remain bounded.
- [ ] OPE support, effective sample size, importance-weight, and estimator-agreement gates pass.
- [ ] Shadow disagreement is reviewed by operation and task domain.
- [ ] Pass rate and adverse-event rate meet frozen non-inferiority thresholds.
- [ ] Runtime p95 scoring latency fits the control-plane budget.
- [ ] The artifact digest and promotion decision are recorded before canarying.
