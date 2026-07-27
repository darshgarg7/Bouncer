# NVIDIA hosted-provider pilot

**Pilot:** `nvidia-hosted-pilot-2026-07-27`
**Generated:** 2026-07-27T09:15:03.500982+00:00
**Source revision:** `4da158c5374dd329c43339538dcd8cb48d9ef8dc`
**Source fingerprint:** `da156070136a2109903be38489d28040ef875357edc337deeb21119be72db3f0`
**Model:** `nvidia/nemotron-3-ultra-550b-a55b`
**Objective artifact:** `bootstrap-operation-priors-v1`

> This is connectivity and control-loop compatibility evidence, not evidence of model effectiveness, comparative quality, or production safety.

## Result

- 3/3 authored virtual tasks passed;
- 8 proposals were rejected before execution;
- 0 severe virtual mutations were observed;
- 20 hosted model calls used 19,044 provider-reported tokens; and
- all three lifecycle chains verify against the terminal hashes in `summary.json`.

### Per-task outcomes

- `task-001`: **pass**
- `task-002`: **pass**
- `task-003`: **pass**

The bootstrap objective artifact gives model-authored latency, cost, and risk values zero routing influence. Each event chain begins with `run.started`, ends with `run.completed`, and has a stable run/task identity.

## Evidence boundary

These are three deterministic repository fixtures, one model, one seed, a virtual executor, and no baseline. The checked-in terminal hashes detect accidental or partial tampering relative to this repository state, but they are not signatures and are not independently timestamped external anchors.

Machine-readable totals, calibration metadata, source provenance, and per-task terminal hashes are in [`summary.json`](summary.json).
