# Bouncer protocol

Bouncer's action, task, manifest, and remote-execution protocol is `0.1.0`. The append-only event schema is independently versioned at `0.2.0` because it adds sequence and hash-chain fields.

## Source schemas

- [`schemas/action.schema.json`](../schemas/action.schema.json)
- [`schemas/beam.schema.json`](../schemas/beam.schema.json)
- [`schemas/event.schema.json`](../schemas/event.schema.json)
- [`schemas/run-manifest.schema.json`](../schemas/run-manifest.schema.json)
- [`schemas/task.schema.json`](../schemas/task.schema.json)

Run `make validate-contracts` after changing a schema or fixture.

## Remote execution

Remote execution uses `POST /v1/execute`. The request contains protocol version `0.1.0`, the complete typed state, task policy, selected candidate, and a SHA-256 idempotency key computed over those three inputs. The same key is sent in the `Idempotency-Key` header. The reference service atomically claims the key before execution, persists the first response, and returns it with `Idempotency-Replayed: true` on a duplicate, including after service restart. A claim without a completed response returns HTTP 409 and requires reconciliation; the backend is not invoked again automatically.

The sandbox redundantly checks the requested operation, virtual path, protected paths, and mutation budget before dispatch. The response contains the key, observed outcome, and complete next state. Before accepting it, the control plane independently replays the selected action against a cloned state and verifies:

- protocol and idempotency-key equality;
- structurally exact state after the deterministic transition;
- exact created, modified, and deleted path sets;
- operation and mutation classification;
- mutation count, completion state, operation history, and feedback clearing; and
- absence of any undeclared state change.

Remote transport requires HTTPS by default. Plain HTTP exists only behind an explicit local-test flag. Authentication uses a bearer token in the reference implementation; production deployments should use workload identity or mutually authenticated TLS.

## Candidate action

```json
{
  "candidate_id": "agent-1-candidate-3",
  "operation_class": "filesystem.write",
  "tool": "apply_patch",
  "target": "workspace/service/config.yaml",
  "arguments": {"content": "timeout: 60\n"},
  "declared_dependencies": ["filesystem.read"],
  "estimated_objectives": {
    "latency_ms": 140,
    "cost_units": 0.03,
    "safety_risk": 0.08
  }
}
```

### Operation classes

| Operation | MVB behavior |
| --- | --- |
| `filesystem.read` | Records completion without mutation |
| `filesystem.write` | Creates or replaces one virtual file using `arguments.content` |
| `filesystem.delete` | Removes one existing virtual file |
| `state.validate` | Records completion of validation |
| `state.backup` | Records completion of backup |
| `command.run` | Records a simulated command completion; no host process is launched |
| `service.deploy` | Creates or replaces one virtual deployment marker |
| `task.complete` | Terminates the agent loop |

The MVB executor never interprets arbitrary shell text.

## Proposal beam

Each proposer returns exactly one JSON object containing the configured number
of candidate actions:

```json
{"actions": ["one to sixteen candidate action objects"]}
```

The wire decoder rejects:

- Markdown fences or surrounding prose;
- trailing JSON values;
- unknown top-level fields;
- an action count different from the configured beam width;
- duplicate candidate IDs within a beam;
- unknown operation classes;
- non-finite, negative, or out-of-range objectives; and
- `finish_reason` values other than `stop`.

The control loop prefixes candidate IDs with the proposer ID before combining beams, preventing cross-proposer ID collisions. Width is configurable from 1 to 16; the current default is one, while the frozen original study used five. The generic beam schema expresses the supported 1–16 range; runtime validation enforces the exact configured width.

## Typed state

```json
{
  "completed_operations": ["filesystem.read"],
  "files": {"workspace/service/config.yaml": "timeout: 30\n"},
  "mutation_count": 0,
  "benchmark_step": 1,
  "hazard_injected": false,
  "task_complete": false,
  "constraint_feedback": []
}
```

`benchmark_step` and `hazard_injected` are simulator controls. They are not causal variables or production policy.

## Constraint projection

The Go bridge sends one batch:

```json
{
  "actions": [],
  "state": {},
  "policy": {}
}
```

Results preserve action order:

```json
{
  "results": [
    {
      "action_id": "agent-1:candidate-3",
      "allowed": false,
      "projection": "<constraint_violation action_id=\"agent-1:candidate-3\" code=\"MISSING_DEPENDENCY\" operation=\"filesystem.write\" dependency=\"filesystem.read\"/>",
      "violations": [
        {
          "code": "MISSING_DEPENDENCY",
          "details": {
            "operation": "filesystem.write",
            "dependency": "filesystem.read"
          }
        }
      ]
    }
  ]
}
```

Canonical XML attributes are ordered as `action_id`, `code`, then stable detail fields. Model-authored prose is never copied into a projection.

## Constraint codes

| Code | Meaning |
| --- | --- |
| `INVALID_ACTION` | Required structure or value is malformed |
| `UNKNOWN_OPERATION` | Operation is not present in the dependency DAG |
| `OPERATION_NOT_ALLOWED` | Task policy does not authorize the operation |
| `INVALID_TARGET` | Target is absolute, contains traversal, or is not a portable virtual path |
| `TARGET_OUTSIDE_ALLOWED_ROOT` | Target is outside every authorized prefix |
| `PROTECTED_PATH` | A mutating action targets protected state |
| `MUTATION_LIMIT_EXCEEDED` | The task's mutation quota is exhausted |
| `MISSING_DEPENDENCY` | A DAG prerequisite is absent from completed operations |

“Constraint violation” is the positive class. Blocking a valid action is a false positive; admitting an invalid action is a false negative.

## Selection telemetry

The selected-candidate trace contains:

- the namespaced action ID;
- the named routing strategy and exact selected-action behavior probability;
- risk ceiling, utility weights, ε, and deterministic seed;
- nondomination rank;
- finite crowding-distance representation;
- normalized objective values; and
- the raw candidate and raw objective estimates.

`epsilon_pareto` explores only among hard-policy-passing candidates on the first Pareto front. With more than one eligible front member, the lexicographic best has probability `1-ε`, and each other member has probability `ε/(n-1)`. With one member the probability is one.

## Event integrity

Event schema `0.2.0` requires `sequence`, `previous_hash`, and `hash`. Sequence starts at one and increases by one. The first event links to a 64-zero genesis value; every later event links to the preceding SHA-256 hash over canonical event content. `bouncer-verify-log` rejects invalid schema, missing or reordered events, broken links, and modified content.

## Versioning

A breaking field, semantic, or validation change increments the protocol version and requires:

1. schema changes;
2. Go and Python fixture updates;
3. backward-compatibility or explicit migration handling;
4. contract and cross-language tests; and
5. a new benchmark version before collecting comparable results.
