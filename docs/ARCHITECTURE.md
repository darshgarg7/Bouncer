# Bouncer architecture

Package responsibilities and mechanically enforced dependency direction are
listed in [Package Ownership](PACKAGE_OWNERSHIP.md). The lower authorization and
execution boundaries do not import orchestration or learned-routing packages.

This document describes the implemented system. “Implemented” does not imply production qualification; evidence levels are tracked in [CLAIMS.md](CLAIMS.md).

## Design invariants

1. Model output is untrusted data and has no authorization authority.
2. The canonical Go policy is deterministic and fail closed.
3. Routing can choose only from candidates admitted by policy.
4. The router accepts trusted routing objectives, never provider estimates directly.
5. An execution response is accepted only if it matches the selected action's deterministic transition contract.
6. Every selection policy has explicit semantics and a logged behavior probability.
7. Statistical analysis is offline and cannot add permissions.

## Runtime topology

```mermaid
flowchart LR
    CLI["bouncer-run"] --> M["Versioned manifest + task"]
    M --> L["Control loop"]

    subgraph Proposal
        L --> C["Bounded coordinator"]
        C --> P["Provider adapter"]
        P --> D["Strict beam decoder"]
    end

    subgraph Authorization
        D --> G["Canonical Go policy"]
        G -->|"reject"| F["Canonical feedback"]
        F --> C
        G -->|"feasible"| K["Objective calibrator"]
        K --> FEAT["Trusted feature extractor"]
        FEAT --> LM["Optional learned scorer"]
        LM --> PH["Risk gate + Pareto holding"]
        PH --> R["Explicit router"]
        K -->|"learning disabled"| R
    end

    subgraph Execution
        R --> E["Virtual or remote executor"]
        E --> S["Authenticated sandbox"]
        S --> I["Durable idempotency store"]
        S --> W["Virtual or Linux rooted backend"]
        W --> O["Observed state diff"]
        O --> L
    end

    L --> H[("Monotonic hash-chained events")]
    H --> X["Offline evaluation"]
```

The default proposal budget is one proposer returning one action because it won the current policy-held-constant smoke study. Larger beams, multiple proposers, adaptive expansion, and exploratory routing require explicit flags.

## Trust boundaries

| Component | Trusted for | Not trusted for |
| --- | --- | --- |
| Provider | Producing candidate bytes and provider telemetry | Authorization, objective truth, or execution |
| Decoder | Contract shape, bounded size, local numeric validity | Semantic policy |
| Go policy | Declared operation, path, dependency, protection, and mutation rules | Undeclared environmental facts |
| Objective calibrator | Bounding and transforming predictions under a hashed artifact | Claiming bootstrap priors are measured or allowing an action |
| Learned scorer | Predicting five outcomes for policy-admitted candidates under a validated artifact | Adding permissions, overriding a rejection, or treating bootstrap predictions as evidence |
| Router | Reproducing the configured choice among admitted, scored candidates | Reading raw provider estimates or authorizing an action |
| Remote gateway | Protocol authentication and response binding | General operating-system containment |
| Rooted backend | Narrow Linux filesystem mediation | Arbitrary commands, arbitrary network tools, or a formal isolation proof |
| Task oracle | Scoring one fixture | General task correctness |
| Offline estimators | Estimating supported policy values from logged data | Live authorization or hidden-confounding removal |

## Proposal plane

`internal/provider` exposes a provider-neutral `Propose` contract. The proposer receives the task instruction, typed state, and a read-only copy of the declared policy so it can avoid obviously inadmissible operations and targets; the copy has no authorization authority. The OpenAI-compatible adapter implements bounded retry, reasoning-budget dialects, strict response limits, exact finish-reason classification, provider usage, and trace propagation. The recorded adapter returns a response only when task, proposer, seed, instruction, typed state, and declared policy match an immutable record.

`internal/harness` assigns stable proposer identities and seeds, issues concurrent calls under one round deadline, cancels on failure, and returns results in deterministic proposer order. `ProposeRange` lets adaptive mode request a stable subset before expanding.

## Policy plane

`internal/policy` loads and validates the dependency DAG once. It rejects cycles and malformed operations at startup. Per candidate it evaluates:

- strict action shape and supported operation class;
- the task operation allowlist;
- portable relative virtual paths;
- allowed-root containment;
- protected-path mutation;
- mutation budget; and
- DAG and declared prerequisites.

Violations are sorted and serialized canonically. The Python package implements the same contract as an independent differential reference. `make verify-policy-parity` runs 100,000 generated cross-language cases.

## Objective-calibration boundary

The action contract retains `estimated_objectives` exactly as the provider authored them. `internal/calibration` creates a separate `ScoredCandidate` for routing. Its artifact defines:

- finite input caps for latency, cost, and risk;
- non-negative affine transforms for continuous latency and cost;
- Platt scaling over bounded risk log odds;
- an operation-level prior with a required fallback; and
- a per-objective model-influence weight in `[0, 1]`.

The blend is `(1 - weight) × prior + weight × transformed estimate`. The checked-in bootstrap uses zero weight, so changing only a model's self-reported objectives cannot change its routing score. The promotion workflow requires nonzero weights to come from the offline fitter, which chooses the smallest weight that improves held-out squared error over the prior. Artifact ID, file digest, provenance, raw input, bounded input, transformed value, prior, and final routing objectives are logged.

The bootstrap operation priors are conservative engineering values, not empirical measurements. The runtime fails at startup if the artifact is missing, malformed, contains unknown fields, lacks a fallback prior, or specifies invalid bounds/transforms.

## Routing plane

All candidates first pass the calibrated risk ceiling. Available policies are:

| Strategy | Selection semantics | Behavior probability |
| --- | --- | ---: |
| `first_valid` | First admitted proposal in stable input order | 1 |
| `lexicographic` | Lowest risk, then cost, latency, and candidate ID | 1 |
| `weighted_utility` | Lowest normalized weighted sum | 1 |
| `pareto_utility` | First nondominated front, then weighted utility | 1 |
| `random_safe` | Uniform over all admitted candidates | `1/n` |
| `epsilon_pareto` | Lexicographic best with `1-ε`; otherwise uniform among other Pareto-front candidates | Exact selected-action propensity |
| `legacy_crowding` | Nondomination rank, crowding, then ID | 1; historical replay only |

Crowding distance remains ranking metadata; it is not the default utility. Adaptive expansion uses the number of valid candidates and calibrated objective-space spread. It logs every trigger and extra request.

### Learned routing path

The optional learning path is independently promoted as `disabled`, `shadow`,
or `active`. It consumes only the calibrated, policy-admitted candidate set.
The portable artifact contains independent generalized-linear models for
progress, terminal success, latency, cost, and adverse risk plus a smoothed
first-order transition prior. Progress and success use lower confidence bounds;
latency, cost, and risk use upper confidence bounds.

Candidates outside the learned risk or uncertainty thresholds are removed.
The router computes nondomination across all five conservative objectives,
limits an oversized frontier by objective-space crowding, and applies an
explicit safety-first selector. Shadow mode records the alternative action and
disagreement without changing execution. Active mode fails the run when model
validation or frontier construction fails. Neither mode can restore a candidate
rejected by policy.

The hand-authored bootstrap learning artifact is restricted to shadow mode.
Learned artifacts are trained offline, loaded immutably, hashed, and never
updated within a run.

## Execution plane

The virtual executor defines the typed state-transition contract. Remote execution sends the state, policy, candidate, and SHA-256 idempotency key to `/v1/execute`. The sandbox:

1. authenticates and bounds the request;
2. validates the idempotency header and request hash;
3. redundantly checks operation, root, protected path, and mutation policy;
4. atomically claims a new key before the backend runs;
5. serializes duplicate execution within the reference service;
6. returns a durable cached response when the key already completed;
7. fails closed on a claimed key with no completed response; and
8. atomically persists the response before returning it.

The caller independently replays the action against a cloned state and rejects any mismatch in next state or outcome.

On Linux, the optional rooted backend uses `openat2` with `RESOLVE_BENEATH`, `RESOLVE_NO_MAGICLINKS`, and `RESOLVE_NO_SYMLINKS`. It accepts regular files with one link, bounds I/O, and refuses unrestricted commands. The deployment template places it behind gVisor with non-root execution, dropped capabilities, read-only root, quotas, persistent idempotency storage, and default-deny egress. External qualification is still required.

## Evidence and telemetry

Event schema `0.2.0` includes a monotonically increasing sequence, previous hash, and current SHA-256 hash over canonical event content. `bouncer-verify-log` verifies schema, sequence, content, and every chain link.

OpenTelemetry spans cover proposal, projection, routing, and execution. W3C trace context propagates to provider and sandbox HTTP requests; trace and span IDs are added to decision events without capturing prompt content. The sandbox exposes Prometheus text metrics at `/metrics`.

## Offline evaluation boundary

`benchmarking.ope` implements IPS, self-normalized IPS, clipped IPS, and doubly robust estimates with deterministic bootstrap intervals. It refuses admission when target support is incomplete, effective sample size is too low, importance weights are too large, or estimators disagree beyond the frozen threshold. This package has no dependency edge into policy or execution.

## Failure semantics

- `finish_reason: length` is always truncation, even if partial content parses.
- Unknown or malformed beams fail the proposal; no repair prompt runs implicitly.
- A failed proposer fails its requested range.
- Missing or invalid objective calibration fails the run before routing.
- Policy or persistence errors fail closed.
- No valid candidate causes canonical feedback and replanning, never fallback execution.
- A mismatched sandbox response cannot update caller state.
- A corrupt idempotency record or event link is an error, not a cache miss.
