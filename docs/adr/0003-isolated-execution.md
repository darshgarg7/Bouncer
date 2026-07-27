# ADR 0003: Real tools require ephemeral isolated workers

**Status:** Accepted, reference implementation complete; qualification pending
**Date:** 2026-07-26

## Context

The reference remote service executes only virtual state. A normal container alone is not a sufficient boundary for arbitrary model-selected workloads.

## Decision

Real-tool execution will use ephemeral Linux workers under gVisor `runsc` or a separately approved microVM runtime. Workers run non-root with a read-only root, explicit mounts, default-deny network, resource quotas, bounded output, forced teardown, and no host control socket.

Tools are exposed through narrow adapters. An unrestricted shell is not the first production backend.

The implemented Linux backend exposes only rooted filesystem read/write/delete plus state-machine operations. It uses `openat2` beneath/no-symlink resolution, rejects hard-linked targets, bounds I/O, and explicitly rejects `command.run`. The Kubernetes template selects `runsc`, drops all capabilities, uses a read-only root, bounds writable volumes and resources, and denies egress. These are mechanisms, not qualification evidence.

## Consequences

- macOS development remains virtual.
- Linux-specific integration and adversarial tests are required.
- Real execution remains disabled until independent security review resolves all critical and high findings.
