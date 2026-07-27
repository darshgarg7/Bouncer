# ADR 0004: Run evidence is ordered and tamper evident

**Status:** Accepted
**Date:** 2026-07-26

## Context

Independent event IDs and append-only file creation do not reveal deletion, reordering, or modification after a run.

## Decision

Every run event receives a monotonic sequence number, the previous event hash, and an event hash computed over canonical event content. The terminal event commits the aggregate result fields emitted by the runner. Verification is a release and analysis prerequisite; a future release bundle separately records manifest and artifact hashes.

This hash chain detects tampering; it does not prove that the producing host was uncompromised. Release signing and provenance address artifact identity separately.

## Consequences

- Event schema and writer state become versioned protocol components.
- Concurrent writers must serialize through one run ledger.
- Analysis refuses incomplete or invalid chains.
