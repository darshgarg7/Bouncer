# Recovery and reconciliation

Bouncer's reference sandbox stores one `<idempotency-key>.claim` before backend
execution and one `<idempotency-key>.json` only after the complete response is
durable. A claim without a response is intentionally indeterminate: the
operation may have happened even though its result was not recorded.

## Backup and restore

1. Stop new execution traffic and wait for the HTTP server's graceful-shutdown
   window to end.
2. Take a filesystem snapshot of the idempotency directory and any executor
   workspace as one consistency unit. Preserve ownership, mode bits, and the
   storage provider's snapshot identifier.
3. Copy event logs and their externally stored final-hash anchors to a separate
   append-only store.
4. Restore into an empty directory, mount it read-only for inspection, and run
   strict JSON decoding over every response record before enabling traffic.
5. Compare restored response keys with the response body, event identities, and
   external anchors. Any disagreement keeps remote execution disabled.

The in-memory store is a development fixture and has no recovery guarantee.

## Indeterminate execution

Never delete a lone claim merely to make a retry proceed. Instead:

1. quarantine the affected worker and key;
2. identify the candidate and pre-execution state from the authenticated request
   and event log;
3. inspect the target system using an independent read path;
4. determine whether the exact authorized transition happened, did not happen,
   or remains unknowable;
5. record the operator, evidence, decision, and a new external anchor; and
6. repair the record only with a reviewed administrative tool. The reference
   server deliberately does not expose such an endpoint.

If absence of the mutation cannot be established, leave the key indeterminate
and escalate. At-least-once retries are unsafe for an unknown non-idempotent
effect.

## Storage corruption

Malformed, mismatched, or trailing content in a response record fails closed.
Retain the damaged bytes, storage diagnostics, and snapshot metadata. Restore a
known-good snapshot or reconcile each affected key; do not hand-edit JSON in
place.

## Credential rotation

Rotate the sandbox bearer token at the secret manager, restart workloads so no
process retains the old value, invalidate provider credentials separately, and
review authentication failures plus event-chain anchors for the exposure
window. See [SECURITY.md](../SECURITY.md) for incident handling.
