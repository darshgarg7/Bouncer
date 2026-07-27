# NVIDIA hosted-provider pilot

**Pilot:** `nvidia-hosted-pilot-2026-07-27`
**Generated:** 2026-07-27T06:29:25.981804+00:00
**Source revision:** `e3a5df1b383fb870f31a84f12cd416f20a668fa1`
**Source fingerprint:** `e768534e61b175933efa48274f53a7fa5e111c97b206894f8a673cdd6f012e25`
**Model:** `nvidia/nemotron-3-ultra-550b-a55b`
**Objective artifact:** `bootstrap-operation-priors-v1`

> This is connectivity and control-loop compatibility evidence, not evidence of model effectiveness, comparative quality, or production safety.

## Result

- 2/3 authored virtual tasks passed;
- 8 proposals were rejected before execution;
- 0 severe virtual mutations were observed;
- 23 hosted model calls used 23,296 provider-reported tokens; and
- all three lifecycle chains verify against the terminal hashes in `summary.json`.

### Per-task outcomes

- `task-001`: **fail** — task did not emit task.complete
- `task-002`: **pass**
- `task-003`: **pass**

The bootstrap objective artifact gives model-authored latency, cost, and risk values zero routing influence. Each event chain begins with `run.started`, ends with `run.completed`, and has a stable run/task identity.

## Evidence boundary

These are three deterministic repository fixtures, one model, one seed, a virtual executor, and no baseline. The checked-in terminal hashes detect accidental or partial tampering relative to this repository state, but they are not signatures and are not independently timestamped external anchors.

Machine-readable totals, calibration metadata, source provenance, and per-task terminal hashes are in [`summary.json`](summary.json).
