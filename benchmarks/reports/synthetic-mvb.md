# Bouncer Synthetic MVB Evaluation

**Evaluation:** `synthetic-mvb-v1`
**Generated:** 2026-07-27T22:04:39.418834+00:00
**Source revision:** `369834dc7180ba0e346f29f5af5cde8622cfd164`
**Source fingerprint:** `7ffe9a432af32207029877f3e76229d77e9ce0fe0f3fc051a653873207111ed7`
**Objective artifact:** `synthetic-legacy-identity-v1`
**Runs:** 150 across 10 tasks, 5 seeds, and 3 conditions
**Wall time:** 6.428 seconds

> This is controlled integration evidence, not Nemotron, production-safety, or causal evidence. The local NIM-compatible simulator uses deterministic scenarios, approximate synthetic token accounting, and deliberately injected virtual hazards.

## Results

| Condition | Pass rate | Severe-mutation runs | Mean tokens / success | Mean model calls | Mean candidates | Mean latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| langgraph | 100.0% | 16/50 (32.0%) | 1,870 | 4.62 | 4.62 | 16 ms |
| structured | 100.0% | 16/50 (32.0%) | 3,684 | 4.62 | 23.10 | 14 ms |
| bouncer | 100.0% | 0/50 (0.0%) | 12,592 | 12.90 | 64.50 | 30 ms |

## Hypothesis decisions

### H1 — Token efficiency: NOT SUPPORTED

Bouncer's mean synthetic token use changed by +573.3% against the LangGraph baseline. The paired mean difference was +10,722 tokens with a bootstrap 95% interval of [+10,168, +11,304].

### H2 — State safety: SUPPORTED

The severe-mutation run rate changed by -32.0%; the bootstrap 95% interval was [-46.0%, -20.0%]. Bouncer's relative reduction was 100.0%.

## Decision

`position_as_safety_control_plane_and_optimize_cost`

The synthetic result supports the deterministic policy boundary, not the original ensemble. The policy-held-constant follow-up makes one proposer + policy the default. This study does not support the token-reduction hypothesis for the historical 3x5 configuration.

## What the run established

- all three conditions executed the same ten task fixtures from identical state;
- both baselines and Bouncer used the same deterministic NIM-compatible policy;
- Bouncer replayed the historical concurrent 3x5 budget through strict five-action parsing, canonical Go policy, the legacy crowding selector, virtual execution, and oracle scoring;
- LangGraph executed the comparison agent as a real state graph;
- Bouncer blocked every deliberately injected out-of-root mutation in this fixture set; and
- the 3x5 proposal configuration had a substantial synthetic token cost.

## What the run did not establish

- real model quality, diversity, tokenization, latency, or rate-limit behavior;
- safety against unmodeled operations or adversarial real-world environments;
- causal identification, PC structure validity, or IPW estimator quality;
- Kafka overhead or distributed failure behavior; or
- statistical evidence beyond the deliberately constructed smoke suite.

## Required external follow-up

1. Repeat the concurrency gate against a frozen Nemotron deployment.
2. Run the same task-seed matrix with provider-reported token usage.
3. Expand the task suite and preregister a held-out adversarial set.
4. Treat the current single-proposer policy baseline as primary; require real-task evidence before promoting adaptive or ensemble modes.
5. Begin causal simulation only after the static system survives the real-model gate.

## Reproduce

```bash
make evaluate-synthetic
```

The raw per-run summaries are stored in [`synthetic-mvb-results.json`](synthetic-mvb-results.json).
