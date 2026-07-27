# Candidate and project brief

## Recruiter snapshot

| | |
| --- | --- |
| **Candidate** | Darsh Garg |
| **Project** | Bouncer — personal AI-infrastructure and security research prototype |
| **Role fit** | Backend, platform, distributed systems, AI infrastructure, and application security |
| **Core stack** | Go, Python, HTTP, OpenTelemetry, Prometheus, Docker, Kubernetes, GitHub Actions |
| **Fast proof** | `make demo` completes five control-boundary checks without credentials or network access |

## 30-second description

Bouncer is a Go control plane that treats AI-agent tool calls as untrusted
proposals. It applies deterministic authorization before execution, verifies
the resulting state transition, and records lifecycle-complete audit evidence.
The project demonstrates systems design, security reasoning, cross-language
testing, experimental discipline, and release engineering—not just model API
integration.

## Evidence at a glance

| Evidence | Signal |
| --- | --- |
| **100,000 generated cases** matched between the Go policy and independent Python oracle | Cross-language contract design and differential testing |
| **82.3% Linux / 83.0% macOS overall Go coverage**; Linux's `openat2` executor is **81.6%**, while the other critical packages exceed 90% | Failure-path and boundary-focused testing with platform-specific ratchets |
| **5/5 credential-free demo checks** | A reviewer can verify the central idea immediately |
| **3/3 hosted pilot tasks**, with rejected proposals not executed | Real-provider integration, honestly scoped as connectivity evidence |
| A 3×3 ensemble used **3.35×** mean synthetic tokens without improving fixture results | Measurement changed the design; complexity was removed from the default |

## What Darsh owned

- Framed the authorization problem and selected the trust boundaries.
- Designed the Go runtime, Python oracle/evaluation boundary, schemas, and
  evidence model.
- Chose the experiments and retained negative and failed results.
- Defined the release, security, compatibility, and claim-promotion gates.
- Used AI coding assistants transparently for drafting and review while
  retaining responsibility for explaining, testing, changing, and defending
  the resulting system.

See [Project History & Authorship](PROJECT_HISTORY.md) for the full account.

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
single-proposer policy path became the default. One hosted pilot passed only
2/3 task oracles and exposed a strict-completion failure; the current
source-bound rerun passed 3/3. Both remain connectivity evidence, not
effectiveness.

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
   and honest provider-pilot history, including both 2/3 and 3/3 runs.
6. **Learned routing (1.5 minutes):** trusted features, conservative objectives,
   shadow mode, and why policy authority is unchanged.
7. **Limitations and next experiment (1.5 minutes):** external benchmarks,
   multiple models/seeds, containment testing, and independent reproduction.

## Honest résumé bullets

- Built a Go authorization and execution control plane for model-proposed tool
  calls, including fail-closed policy, transition verification, durable
  idempotency, and lifecycle-complete hash-chained evidence.
- Differentially tested the Go policy against an independent Python oracle
  across 100,000 generated cases; achieved 82.3% Linux / 83.0% macOS overall Go coverage,
  above 90% in policy, routing, and anomaly boundaries, and 81.6% for the
  Linux-only rooted executor package.
- Measured a 3.35× mean synthetic-token overhead from a 3×3 proposal ensemble
  without improved fixture results, then removed the unearned complexity from
  the default architecture.
- Implemented shadow-gated learned routing and anomaly scoring with portable
  artifacts, uncertainty and validation gates, and no ability to override
  deterministic authorization.

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
