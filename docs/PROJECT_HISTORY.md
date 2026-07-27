# Project history and authorship

## Authorship statement

Bouncer is Darsh Garg's personal engineering project. Darsh owns the project
scope, architectural decisions, claim boundaries, experiment selection, and
the decision to publish or promote any result.

Development used ordinary engineering tools—including Git, linters, test
runners, documentation tools, and AI coding assistants such as Codex—for code
drafting, refactoring, test generation, review, and writing support. Generated
work was inspected in the repository and exercised through the same tests and
publication gates as manually typed work.

The project does not claim reliable line-by-line attribution between manual and
AI-assisted typing. That distinction is not recoverable after iterative edits.
The meaningful ownership claim is that the author can explain, test, modify,
and defend the checked-in system and accepts responsibility for its errors.

## Original hypothesis

The project started from a narrow question:

> If tool calls are treated as untrusted proposals, does a deterministic policy
> boundary account for more of the observed safety value than expensive
> multi-proposer routing?

The first implementation explored multi-proposer beams, scalar utility,
Pareto-style routing, and synthetic comparisons. It deliberately injected
hazardous proposals so the authorization boundary could be exercised.

## Failures that changed the design

### Ensemble complexity did not earn its cost

In the controlled mechanism study, the fixed 3×3 ensemble used 3.35× the mean
synthetic tokens of the single-proposer default while producing the same fixture
completion and severe-mutation counts. Adaptive expansion fired on every turn
and matched the fixed ensemble's cost. The default was simplified; the wider
strategies remain experiments.

### Model-authored objective values were not trustworthy routing inputs

The early design bounded model-provided latency, cost, and risk values, but a
monotonic transform still preserved dishonest ordering. Bouncer introduced a
type-separated objective boundary and a bootstrap calibration artifact with
zero model influence. Nonzero influence now requires held-out measured evidence.

### Real-provider completion was stricter than connectivity

The first hosted attempt established API connectivity and strict parsing, but
the model repeatedly produced invalid `task.complete` targets. A later
three-task pilot passed only 2/3 task oracles: one task made the requested file
change but still failed the protocol-level completion requirement. The current
source-bound rerun passed 3/3, but the repository preserves the 2/3 result as a
failure rather than converting partial progress into success or pooling runs.

### Evidence integrity needed lifecycle semantics

Hash links alone were insufficient. The verifier was strengthened to require
`run.started`, one run/task identity, unique event IDs, monotonic sequence
numbers, and exactly one terminal `run.completed` or `run.failed` event. The
documentation also stopped describing a hash chain as a signature.

## Development history

The repository contains a large early commit followed by substantial rapid
iteration. That history is real and is not rewritten to manufacture a more
conventional narrative. New work is developed on focused branches and split by
subsystem so future review reflects genuine incremental development.

## What the author should be able to defend

- why schemas validate structure but do not grant authority;
- why `openat2` narrows path resolution but does not prove full containment;
- why ambiguous backend failures create indeterminate idempotency records;
- why cross-language parity finds drift but cannot prove policy completeness;
- why hash chains need external anchors and are not signatures;
- why off-policy evaluation requires support for challenger actions; and
- why learned routing cannot restore an action rejected by policy.

See [Hiring Guide](HIRING_GUIDE.md) for concise explanations and
[System Design Walkthrough](SYSTEM_DESIGN_WALKTHROUGH.md) for the long form.
