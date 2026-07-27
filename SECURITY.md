# Security model

Bouncer is an experimental control plane. It is not a production sandbox or a formal verification system.

## Current safety boundary

- Benchmark execution occurs in an in-memory virtual filesystem.
- `command.run` never launches a host process.
- Candidate targets are virtual POSIX paths.
- Absolute paths, traversal components, and backslashes are rejected.
- Mutating actions outside allowed prefixes or against protected paths are rejected.
- Missing DAG prerequisites block execution.
- The canonical Go policy fails closed; Python modes remain independent reference paths and fail closed on process or protocol errors.
- Learned and causal components are not connected to live authorization.
- Remote execution requires HTTPS unless an explicit local-test override is supplied.
- Every remote execution is bound to an idempotency key, receives a redundant task-policy check, is durably replayed across reference-service restarts, and must exactly match a locally replayed state transition before caller state is replaced.
- Event logs use monotonic sequence numbers and a SHA-256 chain; verification fails on content or ordering changes.
- On Linux, the optional rooted backend uses beneath/no-symlink `openat2` resolution, denies hard-linked targets, bounds I/O, and refuses unrestricted commands.

## Threats considered

- malformed or trailing model JSON;
- valid JSON carrying an unauthorized action;
- path traversal and out-of-root writes;
- mutation of protected fixture state;
- missing operational prerequisites;
- prompt injection copied through model-authored explanations;
- cross-proposer candidate-ID collisions;
- truncated outputs that happen to parse;
- retry amplification; and
- evaluator failure being mistaken for approval.

## Known limitations

- The dependency DAG is only as complete as its declarations.
- Objective estimates are untrusted predictions.
- The default virtual executor does not model operating-system races, permissions, subprocesses, networks, or real credentials.
- Protected paths currently prevent mutation; read-denial policy requires a separate explicit rule before production use.
- The mock NIM contains deliberate hazards and is not an adversarial model.
- No causal estimate has authorization authority.
- The reference sandbox defaults to the virtual backend. The optional Linux rooted broker and gVisor deployment template have not completed independent adversarial or operational qualification.
- The rooted broker anchors deletion to a validated parent descriptor, but it does not exclude another process replacing the final directory entry concurrently. Real use requires an isolated workspace with no concurrent untrusted writer.

## Credential handling

- Supply provider credentials only through `NIM_API_KEY` or an external secret manager.
- Supply the reference sandbox credential only through `BOUNCER_SANDBOX_TOKEN` or an external secret manager.
- Never add credentials to manifests, task fixtures, traces, or benchmark reports.
- `.env` is ignored, but ignoring a file is not a substitute for secret scanning.
- Treat model response bodies and traces as potentially sensitive in real deployments.

## Before real tool execution

Before enabling real tools, complete the Linux adversarial suite, explicit read-deny policy, per-tool schemas, workload-identity or mTLS authentication, gVisor/runtime qualification, resource and egress validation, audit retention, chaos/recovery exercises, and independent review.

## Reporting a vulnerability

Please report suspected vulnerabilities privately by emailing
`darsh.garg@gmail.com` with the subject `Bouncer security report`. Include the
affected revision, impact, reproduction conditions, and a minimal proof of
concept when it is safe to do so. Do not include credentials, exploit payloads
against live systems, or sensitive logs in a public issue.

This is a research prototype maintained on a best-effort basis. Receipt of a
report will be acknowledged when possible, but no production response-time SLA
is offered.
