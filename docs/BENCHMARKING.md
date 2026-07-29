# Benchmarking & research protocol

This document describes Bouncer's evaluation methodology, synthetic benchmark suites, hosted model pilot procedures, and research protocols.

---

## 1. Benchmarking Overview

Bouncer's evaluation framework measures authorization overhead, policy compliance, token efficiency, and routing performance across three tiers of evidence:

1. **Controlled Fixture Benchmarks:** 50 authored synthetic tasks designed to test deterministic state machine transitions, mutation budgets, path restrictions, and prerequisite ordering.
2. **Hosted Model Pilot Runs:** Live runs using real model endpoints (e.g., NVIDIA Nemotron) frozen via versioned run manifests ([run-manifest.nvidia-hosted.json](../configs/run-manifest.nvidia-hosted.json)).
3. **Differential Policy Validation:** 100,000 generated test cases evaluated across independent Go and Python policy implementations to verify cross-language contract parity.

---

## 2. Real-Evidence Research Protocol

### Core Research Question
Does deterministic action policy and explicit routing improve the safety–capability–compute trade-off of tool-using agents relative to simpler baselines given identical models, tools, permissions, and task state?

### Primary Comparisons
- **Baseline:** Single proposer returning one action with deterministic policy.
- **Treatments:**
  - Unrestricted single proposer;
  - Structured beam, first policy-valid action;
  - Random-safe selection;
  - Lexicographic selection;
  - Scalar weighted utility;
  - Fixed 3×3 and 3×5 proposal ensembles;
  - Pareto reduction followed by explicit utility.

*Key Finding:* Synthetic mechanism studies showed that a fixed 3×3 proposal ensemble consumed **3.35×** the mean synthetic tokens without improving task completion or reducing severe mutations. Ensemble complexity was therefore removed from default execution.

---

## 3. Preregistered Hypotheses

- **H1 (Capability Non-Inferiority):** Bouncer's task-success rate is not more than 2 percentage points below single-proposer plus policy.
- **H2 (Severe Policy Event Reduction):** Reduces severe policy events by at least 50% without violating H1.
- **H3 (Bounded Compute Overhead):** Provider token overhead is bounded within pre-registered limits.
- **H4 (Routing Contribution):** Pareto reduction plus utility outperforms random-safe selection or is removed from default routing.

---

## 4. Running Benchmarks

### Running the Synthetic Mechanism Study
```bash
.venv/bin/python -m benchmarking.evaluate
```

### Running the Hosted Model Pilot Summarizer
```bash
.venv/bin/python -m benchmarking.summarize_pilot
```

### Running the Go/Python Differential Policy Parity Gate
```bash
make verify-policy-parity
```

All generated benchmark reports are stored under `benchmarks/reports/` and bound to exact SHA-256 source fingerprints. Any source code modification invalidates stale report digests automatically during `make release-check`.
