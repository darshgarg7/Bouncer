# Hiring guide

## 30-second recruiter description

Bouncer is a Go-based control plane that treats AI-agent tool calls as untrusted
proposals. It applies deterministic authorization before execution, runs actions
through bounded executors, and records verifiable lifecycle evidence. The
project includes cross-language policy testing, a credential-free live demo,
honest synthetic and hosted-provider results, and an optional learned router
that cannot override policy.

## Two-minute technical explanation

LLM tool schemas validate shape, not permission. Bouncer strictly decodes model
output into typed candidates, evaluates each candidate with a canonical Go
policy, converts untrusted objective estimates into trusted routing scores under
a versioned calibration artifact, and executes only the selected admitted
action. The executor returns an observed state diff, and the run is recorded in
a SHA-256 chain with lifecycle and identity checks.

The project originally explored large proposal ensembles and advanced routers.
A controlled fixture study found no completion or severe-mutation improvement
from a fixed 3×3 ensemble, despite 3.35× mean synthetic token cost, so the
single-proposer policy path became the default. A hosted pilot passed 2/3 task
oracles and exposed a strict-completion failure; that result is reported as
connectivity evidence, not effectiveness.

Optional learned routing runs only after admission. The bootstrap artifact is
shadow-only, and active promotion requires measured outcomes, held-out gates,
support checks, and an explicit configuration change.

## Ten-minute walkthrough

1. **Problem and trust model (1 minute):** schema validity versus authority.
2. **Control loop (2 minutes):** proposal, policy, calibration, routing,
   execution, verification, recording.
3. **Filesystem and remote execution (1.5 minutes):** `openat2`, state identity,
   idempotency, and remaining qualification gaps.
4. **Evidence (1 minute):** lifecycle semantics, hash links, external anchors.
5. **Experiments (1.5 minutes):** synthetic fixtures, negative ensemble result,
   and honest 2/3 provider pilot.
6. **Learned routing (1.5 minutes):** trusted features, conservative objectives,
   shadow mode, and why policy authority is unchanged.
7. **Limitations and next experiment (1.5 minutes):** external benchmarks,
   multiple models/seeds, containment testing, and independent reproduction.

## Honest résumé bullets

- Built a deterministic Go authorization and execution gateway for model-proposed
  tool calls, with strict contracts, path/dependency/mutation policy, transition
  verification, and lifecycle-complete hash-chained evidence.
- Differentially tested the canonical Go policy against an independent Python
  oracle across 100,000 generated cases and raised current Go coverage above 82%
  with race, concurrency, crash-recovery, and malformed-artifact tests.
- Designed controlled routing studies that showed a fixed 3×3 ensemble consumed
  3.35× mean synthetic tokens without improving fixture completion, then removed
  that complexity from the default architecture.
- Integrated a shadow-gated learned-routing substrate with portable GLMs,
  uncertainty/risk gates, Pareto holding, trajectory builders, and off-policy
  evaluation while preserving deterministic policy authority.

## Relevant roles

- backend and distributed-systems engineering;
- platform and infrastructure engineering;
- AI infrastructure and agent-platform engineering; and
- application security or security engineering.

## Hardest decisions to defend

- Prefer an indeterminate idempotency state over a blind duplicate mutation.
- Give model-authored objective estimates zero bootstrap influence.
- Report a file-changing hosted run as failed when protocol completion is invalid.
- Remove ensemble complexity after measuring no fixture-level benefit.
- Call the evidence chain tamper-evident, never signed or tamper-proof.

## Live interview preparation

Run `make demo`, then practice one small change without prepared notes:

- add a new deterministic policy denial and its Go/Python parity case;
- add one malformed event-log test;
- change a shadow-routing threshold and explain why policy authority is unchanged;
- diagnose an indeterminate sandbox key; or
- add one typed field end to end through schema, decoder, event, and verifier.

The goal is not memorization. The author should be able to locate the relevant
package, state the invariant, change it, and run the focused test.
