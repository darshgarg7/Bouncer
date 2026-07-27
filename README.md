# Bouncer

> **A deterministic authorization and evidence layer for AI agents.**

[![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev/)
[![Python Version](https://img.shields.io/badge/Python-3.11%2B-3776AB?style=flat-square&logo=python&logoColor=white)](https://www.python.org/)
[![License](https://img.shields.io/badge/License-MIT-success?style=flat-square)](LICENSE)

AI agents can propose actions. **They should not authorize themselves.**

Bouncer sits between model output and an executor. It strictly decodes proposed
tool calls, enforces deterministic policy, bounds execution, verifies the
resulting state transition, and records tamper-evident lifecycle evidence.

**Go · Python · AI infrastructure · Security · Distributed systems**

[Demo](#run-the-demo) · [Architecture](#architecture) · [Results](#engineering-results) · [Technical walkthrough](docs/SYSTEM_DESIGN_WALKTHROUGH.md) · [Candidate brief](docs/HIRING_GUIDE.md)

## At a glance

- **100,000** generated cases matched across independent Go and Python policy implementations.
- **50/50** authored fixture tasks completed with zero deliberately injected severe virtual mutations.
- **5/5** control boundaries pass in the credential-free live demo.
- **3/3** authored NVIDIA Nemotron pilot tasks completed; five rejected proposals never executed.
- **83.0%** overall Go coverage; policy, router, executor, and anomaly runtime each exceed 90%.
- A fixed 3×3 proposal ensemble used **3.35×** the mean synthetic tokens without improving fixture results.
- **One command:** `make demo`.

## Run the demo

```bash
make demo
```

No credentials, network access, Python environment, Docker, or external model
are required.

```text
DEMO PASSED: 5/5 control boundaries behaved as expected
```

![Credential-free Bouncer terminal demo](docs/assets/bouncer-demo.gif)

The demo exercises real Bouncer packages: malformed-input rejection,
deterministic policy denial, successful bounded execution, event-chain tamper
detection, and learned-routing shadow mode.

## Architecture

```mermaid
flowchart LR
    M["Model proposal"] --> D["Strict decoder"]
    D --> P["Deterministic authorization"]
    P -->|"rejected"| A["Audit evidence"]
    P -->|"admitted set"| R["Calibrated router"]
    R --> E["Bounded executor"]
    E --> V["Transition verification"]
    V --> L[("Hash-chained lifecycle evidence")]
    P --> L
    R --> L
```

### The core invariant

**The model proposes. Bouncer authorizes.**

Every model-generated action crosses a deterministic authorization boundary
before execution. Routing may choose among admitted actions, but no learned,
anomaly, or model-generated signal can restore an action that policy rejected.

## Engineering results

### 100,000-case differential policy validation

The canonical Go authorization policy matched an independently implemented
Python reference across exactly **100,000 generated cases**. This verifies
cross-language behavioral consistency; it does not prove policy completeness.

### 50/50 controlled fixture tasks completed

The default single-proposer system completed all **50 authored fixture tasks**
with **zero deliberately injected severe virtual mutations**. This is a
deterministic synthetic study, not evidence of real-world effectiveness.

### 3/3 hosted-model pilot tasks completed

Using NVIDIA Nemotron, Bouncer completed **all three authored hosted-model
tasks**. Five policy-rejected proposals were not executed, and all three event
chains verified. One model, one seed, and no baseline make this connectivity
evidence—not comparative effectiveness.

### The experiment that made the system simpler

A fixed 3×3 proposal ensemble consumed **3.35×** the mean synthetic tokens while
improving neither fixture completion nor severe-mutation count. I removed it
from the default architecture. The measured result favored a simpler system
whose primary safety boundary is deterministic authorization, not more model
inference.

See the [mechanism study](benchmarks/reports/mechanism.md), [hosted pilot](benchmarks/reports/nvidia-hosted-pilot-2026-07-27/README.md), and [claim register](docs/CLAIMS.md) for source-bound artifacts and exact qualification language.

## Hard problems I had to solve

- **Fail-closed authorization:** malformed, unsupported, and policy-rejected model output never reaches execution.
- **Filesystem containment:** the rooted Linux executor uses `openat2` beneath/no-symlink resolution, denies hard-linked targets, bounds I/O, and refuses unrestricted commands.
- **Ambiguous distributed failure:** an idempotency key becomes indeterminate instead of blindly replaying a mutation whose outcome is unknown.
- **Execution verification:** authorizing an intended mutation is insufficient; the caller independently verifies the executor's observed state transition.
- **Tamper evidence:** execution events form a lifecycle-complete SHA-256 chain, with the explicit limitation that whole-chain replacement requires an external final-hash anchor.
- **Learning without surrendering authority:** learned routing and anomaly scoring operate only around the policy-admitted action set and cannot add permission.
- **Reproducible evidence:** executable or evaluation changes invalidate stale benchmark fingerprints and generated reports automatically.

## Engineering quality

```bash
make release-check
```

**51 Python tests · Go race detector · 83.0% overall Go coverage · 92.9% policy coverage · 94.1% executor coverage · strict mypy · Ruff · `go vet` · fuzz targets · mutation gate · secret scanning · reproducible evidence audit · container builds**

The release gate checks schemas, tests, formatting, static analysis, architecture
direction, coverage ratchets, recruiter-facing metrics, documentation links,
credential patterns, source fingerprints, calibration digests, generated
reports, pilot lifecycles, and external final-hash anchors. Source or evaluation
changes invalidate stale evidence automatically.

## Ownership

**Bouncer is an independent personal engineering project by [Darsh Garg](docs/PROJECT_HISTORY.md).** I designed its architecture and trust boundaries, implemented and evaluated the system, designed the experiments, and own its technical decisions and limitations. Development included AI-assisted tooling; methodology and authorship are documented in the [project history](docs/PROJECT_HISTORY.md).

## Limitations

Bouncer is a research prototype, not a production safety proof. Its strongest
runtime and policy claims are internally reproduced, but stronger conclusions
still require recognized external benchmarks, multiple model families and
seeds, real sandbox qualification, independent reproduction, and external
security review. Learned routing and anomaly detection remain disabled by
default and have not demonstrated real-task effectiveness.

## Documentation

| Start here | Purpose |
| --- | --- |
| [System-design walkthrough](docs/SYSTEM_DESIGN_WALKTHROUGH.md) | Control loop, state machine, and trust boundaries |
| [Threat model](docs/THREAT_MODEL.md) | Assets, attacks, controls, and known containment gaps |
| [Claims and evidence](docs/CLAIMS.md) | Supported wording, evidence levels, and prohibited claims |
| [Development guide](docs/DEVELOPMENT.md) | Setup, package map, testing, and contribution workflow |
| [Production readiness](docs/PRODUCTION_READINESS.md) | Implemented controls versus remaining qualification work |
| [Candidate brief](docs/HIRING_GUIDE.md) | Résumé bullets, role alignment, and interview walkthrough |

The [roadmap](ROADMAP.md), [changelog](CHANGELOG.md), [contributing guide](CONTRIBUTING.md), and [security policy](SECURITY.md) cover project maintenance.

## License

MIT. See [LICENSE](LICENSE).
