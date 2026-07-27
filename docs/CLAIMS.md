# Claim and evidence register

This file is the authoritative boundary between what Bouncer has demonstrated and what it is designed to investigate. Public summaries must not state a stronger claim than the corresponding entry here.

## Evidence levels

| Level | Meaning |
| --- | --- |
| E0 | Design or hypothesis only |
| E1 | Unit, property, or deterministic conformance evidence |
| E2 | Controlled synthetic integration evidence |
| E3 | Real-provider evidence on audited tasks |
| E4 | Reproduced across model families and task domains |
| E5 | Independently reproduced or externally reviewed |

## Supported claims

| ID | Claim | Level | Evidence | Allowed wording |
| --- | --- | ---: | --- | --- |
| C-001 | Bouncer converts model output into a versioned typed-action contract and rejects malformed beams. | E1 | `internal/action`, `internal/nimclient`, schemas, race-tested unit tests | “Bouncer strictly validates typed proposal beams.” |
| C-002 | The canonical Go policy rejects declared operation, dependency, path, protected-resource, and mutation-limit violations, and matches the independent Python reference across 100,000 generated cases. | E1 | `internal/policy`, `constraint_projection`, `make verify-policy-parity` | “Bouncer enforces its declared fixture policy; cross-language parity passed 100,000 generated cases.” |
| C-003 | The original fixed 3×5 configuration completed all 50 synthetic Bouncer runs and executed no deliberately injected severe virtual mutation. | E2 | `benchmarks/reports/synthetic-mvb-results.json` | Must include “synthetic,” the 50-run denominator, and the deliberately injected nature of the hazards. |
| C-004 | The original fixed 3×5 configuration used 698.2% more synthetic tokens than the LangGraph baseline. | E2 | `benchmarks/reports/synthetic-mvb-results.json` | Must be reported beside C-003 when discussing the original comparison. |
| C-005 | In the deterministic integration study, 1×3 used 72.5% fewer synthetic tokens than 3×5 with the same fixture outcomes. | E2 | `benchmarks/reports/synthetic-ablation-results.json` | “The integration ablation selected 1×3; real-provider confirmation is pending.” |
| C-006 | Reusing the Python projector process reduced mean local integration-loop latency from approximately 251 ms to 51 ms without changing fixture decisions. | E2 | `benchmarks/reports/synthetic-projector-ablation-results.json` | Must say “local integration-loop latency.” |
| C-007 | The remote protocol authenticates requests, binds them to an idempotency key, persists and replays the first response across service restarts, and independently validates returned virtual-state transitions. | E1 | `internal/executor`, `internal/sandbox`, restart and collision tests | Must call the checked-in service a reference implementation, not a production sandbox. |
| C-008 | In the policy-held-constant smoke study, single proposer + policy passed 50/50 with zero severe fixture mutations and used fewer synthetic tokens than every tested multi-candidate configuration. | E2 | `benchmarks/reports/mechanism-results.json` | Must say “policy-held-constant smoke study” and “synthetic tokens.” |
| C-009 | Adaptive 1→3×3 passed 50/50 with zero severe fixture mutations and used 456 more mean synthetic tokens than single proposer + policy, versus 5,708 more for fixed 3×3. | E2 | `benchmarks/reports/mechanism-results.json` | May support making adaptive mode experimental; it does not establish real-model value. |
| C-010 | Uniform random-safe routing passed 25/50 in the smoke study and is unsuitable as a default policy. | E2 | `benchmarks/reports/mechanism-results.json` | Present as a negative control result. |
| C-011 | Event verification detects modified or reordered records, and current event logs carry a monotonic sequence and SHA-256 chain. | E1 | `internal/eventlog`, `cmd/bouncer-verify-log`, tests | “Bouncer produces tamper-evident logs”; do not call them tamper-proof. |
| C-012 | The offline estimators recover the known target value within the checked-in contextual-bandit simulation and fail their support/weight gates on invalid input. | E1 | `benchmarking/ope.py`, `benchmarking/ope_simulation.py`, `tests/test_ope.py` | Must say “known-ground-truth simulation”; no real causal claim. |
| C-013 | The Linux rooted executor uses `openat2` beneath/no-symlink resolution, rejects hard-linked targets, bounds I/O, and refuses unrestricted commands. | E1 | `internal/executor/rooted_linux.go`, Linux-only adversarial tests | Must not imply independent sandbox qualification. |

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
