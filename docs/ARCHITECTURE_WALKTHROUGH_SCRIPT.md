# Five-minute architecture walkthrough script

This is a recording script, not a substitute for a live explanation.

## 0:00–0:40 — Problem

Show one schema-valid write outside the allowed root. Explain that schema shape
does not answer whether this action is authorized in the current state.

## 0:40–1:40 — Runtime boundary

Trace proposal → strict decode → Go policy → objective calibration → routing →
executor → observed diff. Emphasize that rejection removes an action permanently.

## 1:40–2:30 — Execution and idempotency

Show the virtual and remote executor interfaces. Explain why a claimed key with
no completed result is indeterminate and requires reconciliation.

## 2:30–3:15 — Evidence

Open one JSONL run. Point out lifecycle events, identity, sequence, previous hash,
and terminal hash. Explain why an external anchor is still required.

## 3:15–4:10 — Experiments

Show the mechanism table and 2/3 hosted pilot. Explain the negative ensemble
result and the strict-completion failure without inflating either study.

## 4:10–5:00 — Learning and limits

Show shadow mode. Explain that learned models rank only admitted candidates and
the bootstrap artifact cannot be activated. End with external benchmarks,
sandbox qualification, and independent review as the next evidence milestones.
