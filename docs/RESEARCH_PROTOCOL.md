# Real-evidence research protocol

**Status:** Draft. This protocol must be versioned and frozen before collecting confirmatory real-provider results.

## Research question

Does deterministic action policy and explicit routing improve the safety–capability–compute trade-off of tool-using agents relative to the strongest simpler system given identical models, tools, permissions, task state, and outcome accounting?

## Primary comparisons

The primary baseline is one proposer returning one action plus the identical deterministic policy and executor. The primary treatment is adaptive Bouncer, beginning with one proposer and requesting a wider beam or more proposers only under frozen triggers. The final adaptive budget is frozen after a real-provider pilot and before confirmatory collection.

Secondary comparisons isolate:

- unrestricted single proposer;
- structured beam, first policy-valid action;
- random-safe selection;
- lexicographic selection;
- scalar weighted utility;
- model judge over the same feasible set;
- fixed 3×3 and 3×5 Bouncer; and
- Pareto reduction followed by explicit utility.

The controlled smoke study is not confirmatory, but it is an engineering stop signal: single proposer + policy was the lowest-cost full-pass configuration; fixed and adaptive 3×3 each added 7,658.48 mean paired synthetic tokens; scalar/Pareto/ε-Pareto passed 0/50 under the zero-influence bootstrap; and uniform random-safe passed 1/50. The real experiment therefore treats all ensemble and advanced routing policies as challengers, never as assumed improvements.

## Hypotheses

### H1 — capability non-inferiority

Adaptive Bouncer’s task-success rate is not more than two percentage points below one proposer plus identical deterministic policy.

### H2 — severe-policy-event reduction

Where the baseline has a measurable event rate, adaptive Bouncer reduces the probability of at least one predefined severe policy event by at least 50% without violating H1.

### H3 — bounded compute overhead

Adaptive Bouncer’s median provider-token use is no more than 15% above one proposer plus identical deterministic policy. Equal-maximum-compute results are reported separately.

### H4 — routing contribution

Pareto reduction plus explicit utility outperforms random-safe selection on the preregistered composite outcome or is removed from the default path.

## Task populations

1. τ-bench retail and airline environments.
2. AgentDojo utility and attack/defense conditions.
3. A held-out BouncerBench set containing multiple policy-valid actions with distinct real trade-offs.
4. An optional fresh software/tool set after independent task-validity audit.

The existing ten synthetic fixtures are conformance tests and are excluded from confirmatory capability or safety analysis.

## Model populations

Use at least three independently developed deployments:

- the target Nemotron deployment;
- a second open-weight deployment; and
- a strong hosted reference.

Exact model IDs, revisions, reasoning settings, endpoints, provider versions, and tokenizer identities are frozen in the experiment manifest.

## Outcomes

### Primary

- task success according to the benchmark oracle;
- run contains a severe predefined policy event;
- total provider-reported input, reasoning, cached, and output tokens;
- provider cost under a frozen price table;
- end-to-end wall-clock latency.

### Secondary

- false-block rate and policy-valid action recall;
- selected-action calibration and regret where an oracle exists;
- retries, throttling, truncation, malformed responses, and timeouts;
- number of proposals and candidates;
- human escalation or abstention;
- deterministic routing latency;
- sandbox failures and unattributed mutations.

## Design

- Paired task, model, and seed comparisons from identical snapshots.
- Randomized condition order where provider caching or temporal drift may matter.
- Pilot tasks excluded from the confirmatory holdout.
- Final sample size chosen from pilot variance and the minimum effects above, targeting at least 80% power.
- At least five seeds per task unless the power calculation requires more.
- Natural-configuration and equal-maximum-compute analyses kept separate.
- Failed and timed-out attempts remain in attempt-level denominators.

## Analysis

- Paired bootstrap confidence intervals for continuous paired outcomes.
- McNemar-style paired analysis for binary task outcomes where applicable.
- Hierarchical model or cluster bootstrap across task and model families as a robustness analysis.
- Holm correction across confirmatory primary comparisons.
- Report absolute effects, relative effects, intervals, denominators, and missingness.
- No pooling across materially different benchmark domains without domain-specific results.

## Exclusions

A run may be excluded only for a preregistered infrastructure failure that prevents every condition from receiving a comparable attempt. Model failures, policy failures, truncation, timeout, and malformed output are outcomes, not infrastructure exclusions.

Task exclusion requires evidence that the task is unsolvable, ambiguous, contaminated, or incorrectly scored. Exclusions are adjudicated without viewing condition identity where practical and are published with reasons.

## Promotion decisions

- If H1 fails, Bouncer is not a default execution policy.
- If H2 fails, no general safety-improvement claim is made.
- If H3 fails, multi-proposer mode becomes an explicit high-risk/extra-compute option.
- If H4 fails, Pareto/crowding machinery is removed from the default path.
- A claim is promoted only on domains and model families where its interval-based gate passes.

## Causal boundary

Confirmatory system comparisons above are randomized paired experiments and do not require causal discovery.

Offline policy evaluation is a separate, later protocol. It requires safe exploration, exact propensities, supported reusable treatments, overlap diagnostics, effective sample size, known-ground-truth simulation, and estimator-robustness checks. No offline estimator may weaken deterministic policy.

## Reproducibility bundle

The release must contain:

- frozen protocol and experiment manifests;
- provider, model, image, policy, task, and code digests;
- raw attempt records and hash-chained events;
- all exclusions and infrastructure incidents;
- deterministic analysis code and generated figures/tables;
- provider cost ledger;
- environment and dependency lockfiles; and
- commands for clean-machine reproduction.
