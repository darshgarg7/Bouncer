# Claim and evidence register

This file is the authoritative boundary between what Bouncer has demonstrated and what it is designed to investigate. Public summaries must not state a stronger claim than the corresponding entry here.

## Evidence levels

| Level | Meaning |
| --- | --- |
| E0 | Design or hypothesis only |
| E1 | Unit, property, or deterministic conformance evidence |
| E2 | Controlled synthetic integration evidence |
| E2P | Real-provider smoke evidence on authored, unaudited tasks |
| E3 | Real-provider evidence on audited tasks |
| E4 | Reproduced across model families and task domains |
| E5 | Independently reproduced or externally reviewed |

## Supported claims

| ID | Claim | Level | Evidence | Allowed wording |
| --- | --- | ---: | --- | --- |
| C-001 | Bouncer converts model output into a versioned typed-action contract and rejects malformed beams. | E1 | `internal/action`, `internal/nimclient`, schemas, race-tested unit tests | “Bouncer strictly validates typed proposal beams.” |
| C-002 | The canonical Go policy rejects declared operation, dependency, path, protected-resource, and mutation-limit violations, and matches the independent Python reference across 100,000 generated cases. | E1 | `internal/policy`, `constraint_projection`, `make verify-policy-parity` | “Bouncer enforces its declared fixture policy; cross-language parity passed 100,000 generated cases.” |
| C-003 | The historical-semantics fixed 3×5 rerun completed 50 synthetic Bouncer runs and executed zero deliberately injected severe virtual mutations. | E2 | `benchmarks/reports/synthetic-mvb-results.json` | Must name the legacy identity artifact and must not compare this result directly with the bootstrap mechanism study. |
| C-004 | The historical-semantics fixed 3×5 rerun used 573.3% more synthetic tokens than the LangGraph baseline. | E2 | `benchmarks/reports/synthetic-mvb-results.json` | Must be reported beside C-003 and identified as synthetic. |
| C-005 | In the deterministic legacy-semantics integration study, 1×3 used 71.1% fewer synthetic tokens than 3×5 with the same fixture outcomes. | E2 | `benchmarks/reports/synthetic-ablation-results.json` | “The integration ablation selected 1×3; real-provider confirmation is pending.” |
| C-006 | Reusing the persistent Python projector reduced mean local integration-loop latency while preserving 50/50 pass rates without changing fixture decisions. | E2 | `benchmarks/reports/synthetic-projector-ablation-results.json` | Report the generated latency values directly; they are machine-dependent timings. |
| C-007 | The remote protocol authenticates requests, binds them to an idempotency key, persists and replays the first response across service restarts, and independently validates returned virtual-state transitions. | E1 | `internal/executor`, `internal/sandbox`, restart and collision tests | Must call the checked-in service a reference implementation, not a production sandbox. |
| C-008 | In the policy-held-constant smoke study, single proposer + policy passed 50/50 with zero severe fixture mutations at 3,256.84 mean synthetic tokens per success, the lowest cost among full-pass conditions. | E2 | `benchmarks/reports/mechanism-results.json` | Must say “policy-held-constant smoke study” and “synthetic tokens.” |
| C-009 | Adaptive and fixed 3×3 each passed 50/50 and added 7,658.48 mean paired synthetic tokens over single proposer + policy; adaptive expansion did not save compute in these fixtures. | E2 | `benchmarks/reports/mechanism-results.json` | This supports removing ensemble complexity from the default; it does not establish real-model value. |
| C-010 | Uniform random-safe routing passed 1/50, while scalar utility, Pareto utility, and ε-Pareto each passed 0/50 under the zero-influence bootstrap. | E2 | `benchmarks/reports/mechanism-results.json` | Present as a negative result showing that static bootstrap priors do not establish routing value. |
| C-011 | Event verification detects modified, reordered, suffix-truncated, or mixed-identity records; complete logs carry a monotonic sequence and SHA-256 chain from `run.started` through one terminal event. | E1 | `internal/eventlog`, `cmd/bouncer-verify-log`, tests | “Bouncer produces tamper-evident logs”; do not call them tamper-proof, and require an external final-hash anchor to detect whole-chain replacement. |
| C-012 | The offline estimators recover the known target value within the checked-in contextual-bandit simulation and fail their support/weight gates on invalid input. | E1 | `benchmarking/ope.py`, `benchmarking/ope_simulation.py`, `tests/test_ope.py` | Must say “known-ground-truth simulation”; no real causal claim. |
| C-013 | The Linux rooted executor uses `openat2` beneath/no-symlink resolution, rejects hard-linked targets, bounds I/O, and refuses unrestricted commands. | E1 | `internal/executor/rooted_linux.go`, Linux-only adversarial tests | Must not imply independent sandbox qualification. |
| C-014 | In the current three-task hosted smoke pilot, Nemotron 3 Ultra passed 2/3 authored virtual tasks; eight rejected proposals were not executed, zero severe virtual mutations occurred, and all three terminal event chains verified. | E2P | `benchmarks/reports/nvidia-hosted-pilot-2026-07-27/summary.json` | Must state one model, one seed, no comparison baseline, and connectivity rather than effectiveness; the earlier 3/3 pilot predates the objective boundary. |
| C-015 | The router's type-level API accepts separately scored objectives rather than raw provider estimates; `configs/objective-calibration.bootstrap.json` gives provider estimates zero influence and logs the full transformation plus artifact digest. | E1 | `configs/objective-calibration.bootstrap.json` | “Bouncer prevents model-authored objective fields from controlling routing under the bootstrap artifact.” Must not call the bootstrap priors empirically calibrated. |
| C-016 | Bouncer can validate a portable learning artifact, score only policy-admitted candidates, apply uncertainty/risk gates, retain a five-objective Pareto set, and run the result in shadow or fail-closed active mode. | E1 | `internal/learning`, `internal/router/learned.go`, active/shadow integration tests | “The gated learned-routing mechanism is implemented and conformance-tested.” Must not claim that the bootstrap artifact or learned routing improves real-task outcomes. |

## Unsupported claims

The following statements are prohibited until their promotion gates are met:

- Bouncer is production safe.
- Bouncer prevents agent failures in general.
- Bouncer reduces real-model token use.
- A multi-proposer ensemble outperforms one proposer plus the same deterministic policy; the current smoke study supports the opposite default.
- Pareto or crowding selection improves task outcomes.
- The objective values emitted by a model are calibrated measurements.
- Execution logs identify causal effects.
- IPW, doubly robust estimation, or causal discovery improves routing.
- The checked-in learning artifact is trained, calibrated, or suitable for active routing.
- Contextual bandits or anomaly detection improve production outcomes.
- The remote reference service safely contains arbitrary host-tool execution.

## Promotion gates

| Claim family | Required evidence before promotion |
| --- | --- |
| Real-model efficiency | Audited real tasks, provider-reported tokens, equal-permission baseline, paired intervals, and replication on two model families |
| Safety improvement | Real isolated execution, predefined violation taxonomy, measurable baseline event rate, interval excluding zero improvement, and no unacceptable pass-rate regression |
| Multi-proposer value | Ablation against one proposer plus identical policy under both equal-compute and natural configurations |
| Routing value | Random-safe, scalar utility, lexicographic, judge, and Pareto-plus-utility comparisons over the identical feasible set |
| Production isolation | Published threat model, adversarial containment suite, operational qualification, and independent security review |
| Causal effect | Supported exploration, exact propensities, overlap and effective-sample-size gates, validated estimator behavior in known-ground-truth environments, and a separate research report |

## Maintenance rule

Any change to a headline number must update, in the same review:

1. the immutable result artifact or its versioned successor;
2. this claim register;
3. the README statement generated from that result;
4. the benchmark interpretation document.

Once versioned release bundles are published, the same review must also update
the release manifest containing the artifact hash.
