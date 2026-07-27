# ADR 0002: Final selection uses an explicit routing objective

**Status:** Accepted
**Date:** 2026-07-26

## Context

NSGA-II crowding distance is designed to preserve diversity within a population. Selecting the maximum-crowding boundary candidate does not by itself express the system’s desired utility or safety preference.

## Decision

Pareto ranking may reduce the feasible set. Final selection must use a named, versioned strategy: first-valid, lexicographic, weighted utility, random-safe control, ε-Pareto exploration, or Pareto followed by explicit utility. Every decision records the strategy, parameters, feasible set, behavior probability, and tie-break.

Crowding distance is retained only for proposal-set diversity and experimental ablation unless evidence supports another use.

The policy-held-constant smoke study selected one proposer plus lexicographic policy as the runtime default. Ensemble, adaptive, Pareto, and exploratory policies require explicit configuration until real-task evidence clears their promotion gates.

## Consequences

- Routing behavior becomes interpretable and testable.
- Weight sensitivity and risk ceilings become part of the benchmark contract.
- The historical crowding-only result remains a frozen integration artifact, not the new default.
- Uniform random-safe routing remains a negative control; it is not a recommended execution policy.
