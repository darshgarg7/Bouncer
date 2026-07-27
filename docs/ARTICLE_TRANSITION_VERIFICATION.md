# Authorizing an action is not enough: verify the transition

An AI agent can propose a valid operation, pass a policy check, and still cause
the wrong state change if the executor is buggy, compromised, or operating on a
different state than the policy evaluated. Bouncer therefore treats
authorization and transition verification as separate boundaries.

## The core problem

Consider an allowed action:

```json
{
  "operation_class": "filesystem.write",
  "target": "workspace/service/config.yaml",
  "arguments": {"content": "timeout: 60\n"}
}
```

Policy admission establishes that this operation and target are permitted at a
particular point in the task. It does not establish that the executor changed
only that file, wrote the requested bytes, respected the mutation quota, or
returned a state derived from the request it received.

## Bouncer's verification loop

The runtime carries a typed state through seven conceptually distinct stages:

1. **Proposal:** obtain a strictly decoded candidate set.
2. **Admission:** evaluate every candidate with the canonical Go policy.
3. **Calibration:** derive trusted routing objectives under a versioned artifact.
4. **Routing:** choose only from the admitted set.
5. **Execution:** submit one candidate and the expected state identity.
6. **Verification:** validate the returned outcome and state transition.
7. **Recording:** append the decision and observed result to one event chain.

The model participates only in the first stage. It cannot relax a later stage.

## Expected versus observed state

The virtual executor applies the selected action to an in-memory typed state and
returns a structured `StateDiff`:

```text
created[]
modified[]
deleted[]
completed_operation
```

The remote executor boundary goes further. Its request includes an idempotency
key derived from the candidate and input state. The response is decoded
strictly, matched to the request identity, and checked before the caller's state
is replaced. A malformed or mismatched response leaves the caller's original
state untouched.

This creates a useful invariant:

> The runtime state advances only after the executor response has been decoded,
> attributed, and validated against the authorized request.

## Why the event chain matters

Every material stage emits an event. Event `N` includes the SHA-256 digest of
event `N-1`, plus its own canonical-content hash. The verifier also checks:

- the first event is `run.started`;
- every event has the same run and task identity;
- sequence numbers are contiguous;
- event IDs are unique;
- no event follows a terminal event; and
- the log ends in exactly one `run.completed` or `run.failed` event.

These semantics catch more than byte edits. They reject reordered events,
suffix truncation, concatenated runs, repeated identities, and incomplete logs.

## What the chain cannot prove

A hash chain is not a digital signature. An attacker who can replace the entire
file can recompute every hash. Bouncer therefore exposes the terminal hash for
storage in an independent location. Comparing against that external anchor
detects complete replacement relative to the anchored run.

The chain also does not prove that the host was uncompromised at event-creation
time. Signed build provenance, workload identity, remote attestation, and
independent log storage are separate controls.

## Idempotency and ambiguous failure

For mutations, “retry on error” can be unsafe. The sandbox claims an idempotency
key before invoking the backend, then records the completed response. If the
process fails between those operations, the durable record is indeterminate:
the backend may or may not have mutated state.

Bouncer refuses to replay such a key automatically. That behavior sacrifices
availability for mutation safety and requires an operator to reconcile the
actual backend state. A production system would add a recovery protocol or a
backend-native transaction identifier; pretending the uncertainty does not
exist would be worse.

## Remaining filesystem race limits

The Linux broker uses `openat2` with beneath/no-symlink resolution and rejects
hard links for its supported operations. This meaningfully narrows traversal
and link attacks, but it does not qualify an arbitrary tool environment.
Mount-namespace behavior, descriptor lifetime, concurrent replacement, rename
races, and kernel/runtime assumptions still require dedicated adversarial tests.

## Practical lesson

Agent safety is not one validation call. The reviewable unit is a loop:

```text
typed proposal -> deterministic admission -> attributed execution
               -> verified transition -> externally anchorable evidence
```

Each arrow is a trust boundary. Keeping those boundaries explicit makes failure
observable and gives the system somewhere principled to stop.
