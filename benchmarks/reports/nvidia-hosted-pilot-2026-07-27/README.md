# NVIDIA hosted-provider pilot

**Pilot:** `nvidia-hosted-pilot-2026-07-27`
**Generated:** 2026-07-27T21:43:00.146077+00:00
**Source revision:** `2d65d2461ff3cb976ccde16f72010fb8cfbdba89`
**Source fingerprint:** `7ffe9a432af32207029877f3e76229d77e9ce0fe0f3fc051a653873207111ed7`
**Model:** `nvidia/nemotron-3-ultra-550b-a55b`
**Objective artifact:** `bootstrap-operation-priors-v1`

> This is connectivity and control-loop compatibility evidence, not evidence of model effectiveness, comparative quality, or production safety.

## Result

- 3/3 authored virtual tasks passed;
- 8 proposals were rejected before execution;
- 0 severe virtual mutations were observed;
- 20 hosted model calls used 18,635 provider-reported tokens; and
- all three lifecycle chains verify against the terminal hashes in `summary.json`.

### Per-task outcomes

- `task-001`: **pass**
- `task-002`: **pass**
- `task-003`: **pass**

The bootstrap objective artifact gives model-authored latency, cost, and risk values zero routing influence. Each event chain begins with `run.started`, ends with `run.completed`, and has a stable run/task identity.

## Evidence boundary

These are three deterministic repository fixtures, one model, one seed, a virtual executor, and no baseline. The checked-in terminal hashes detect accidental or partial tampering relative to this repository state, but they are not signatures and are not independently timestamped external anchors.

Machine-readable totals, calibration metadata, source provenance, and per-task terminal hashes are in [`summary.json`](summary.json).
