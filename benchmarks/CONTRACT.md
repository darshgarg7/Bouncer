# Bouncer benchmark contract

Version `0.1.0` freezes the comparison surface for the Minimal Viable Benchmark.

## Conditions

The LangGraph baseline, structured-output baseline, and static Bouncer condition use the same model deployment, tool descriptions, task instruction, starting state, path permissions, maximum turns, timeout, and oracle. A run manifest records every value needed to reproduce the condition.

## Primary outcomes

- **H1:** total input, reasoning, and output tokens per successful task.
- **H2:** severe state mutations per attempted task, reported with task pass rate.

No token-producing model call is excluded. A task that does not pass has no finite “tokens per successful task” value and remains visible in aggregate pass-rate and attempted-task accounting.

## Secondary outcomes

- false-block rate;
- escaped hard-constraint violations;
- prevented violations;
- provider cost;
- retry count;
- end-to-end p50 and p95 latency; and
- evaluator p50 and p95 latency.

“Constraint violation” is the positive class. Blocking a valid action is a false positive; admitting an invalid action is a false negative.

## Repetition and state

The ten tasks under `benchmarks/tasks/` are integration smoke tests. Comparable evaluation repeats them across frozen seeds from identical materialized states. Each run writes a new append-only JSONL log and never mutates the source task specification.

## Truncation

A proposal is accepted only when the provider returns `finish_reason: "stop"`, the content is exactly one JSON object, the object contains exactly the configured number of actions, all action IDs are unique, and every action passes strict validation. Beam width is bounded from 1 to 16. The frozen original comparison used width five; the current runtime default uses width one. `finish_reason: "length"` is a classified truncation even if the returned prefix happens to parse.

## Preregistration

Effect thresholds and any non-inferiority margin for pass rate must be written into a versioned analysis manifest before comparable benchmark results are collected.
