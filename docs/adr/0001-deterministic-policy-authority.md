# ADR 0001: Deterministic policy owns authorization

**Status:** Accepted
**Date:** 2026-07-26

## Context

Model output, learned scores, objective estimates, and causal estimates are uncertain. Authorization must remain inspectable, reproducible, and fail closed.

## Decision

A canonical deterministic policy engine is the only component that can admit an action to the feasible set. Routing and learned components operate only on that set and cannot expand permissions, remove dependencies, or override a rejection.

The production implementation will live in Go. The Python projector remains a temporary independent reference until differential parity is proven.

## Consequences

- Policy behavior can be fuzzed and embedded in the control plane without a cross-process hot path.
- Redundant execution-side checks must share versioned policy semantics rather than an incomplete handwritten subset.
- Updating policy is a reviewed configuration or code change with a new digest.
