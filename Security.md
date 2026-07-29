# Security policy

## Supported versions

Only the latest published minor release and the default branch receive security
fixes. Before the first release, treat all revisions as development snapshots.

## Reporting

Use GitHub private vulnerability reporting after the repository is published.
Do not include real API keys, private prompts, or sensitive file contents. A
useful report includes the affected revision, executor mode, minimal safe
reproduction, impact, and whether the issue crosses an authorization boundary.

## Response process

1. Acknowledge and reproduce the report privately.
2. Rotate any exposed credentials immediately and invalidate related sessions.
3. Disable affected remote execution paths if unauthorized mutation is possible.
4. Develop a regression test and narrowly scoped fix.
5. Run the publication gate and relevant containment tests.
6. Publish an advisory with affected versions, remediation, and evidence.
7. Reconcile indeterminate idempotency records and audit anchored event logs.

No bounty or response-time SLA is currently offered.
