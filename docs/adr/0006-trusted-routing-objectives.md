# ADR 0006: The router cannot consume model-authored objectives directly

**Status:** Accepted
**Date:** 2026-07-27

## Context

Candidate actions include model estimates for latency, cost, and safety risk.
Numeric validation catches malformed values but says nothing about calibration.
A model can report optimistic numbers, and ordinary Platt or temperature
scaling is monotonic: it may improve probability calibration while preserving
the manipulated ordering. Passing those fields directly to the router therefore
lets the proposer influence its own selection criterion.

## Decision

Keep provider estimates on the raw candidate for audit, but expose a different
`ScoredCandidate` type to the router. A required, versioned artifact:

1. bounds provider inputs;
2. applies affine scaling to latency and cost;
3. applies Platt scaling to safety-risk log odds;
4. looks up a measured or conservative operation prior; and
5. blends each transformed estimate with that prior using an independent
   model-influence weight.

Every decision records the artifact ID and SHA-256, provenance, raw and bounded
inputs, transformed estimates, priors, influence weights, and final routing
objectives. Adaptive objective-spread calculations use the same trusted scores.

The checked-in bootstrap artifact uses zero model influence. Its priors are
explicit engineering defaults, not empirical calibration. The offline fitter
may produce nonzero weights only when transformed estimates improve held-out
loss over the priors; promotion still requires human review of data provenance
and domain coverage.

## Consequences

- A model cannot improve its routing rank by changing only objective metadata
  under the bootstrap artifact.
- The compiler prevents accidental direct use of raw candidate objectives by
  the router.
- Calibration changes are versioned, reviewable, and attributable in evidence.
- Same-operation candidates tie under the bootstrap unless trusted features or
  measured calibration later distinguish them.
- The bootstrap sacrifices some routing recall and optimization quality to
  avoid granting authority to unvalidated estimates.
- A fitted scaler does not become an authorization mechanism; deterministic
  policy remains the only admission boundary.
