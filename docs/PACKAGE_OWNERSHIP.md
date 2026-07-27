# Package ownership and dependency direction

“Ownership” here means architectural responsibility, not separate maintainers.

| Package | Owns | May depend on |
| --- | --- | --- |
| `internal/action` | Provider-neutral candidate contract | Standard library only |
| `internal/benchmark` | Typed tasks and virtual state | Standard library only |
| `internal/policy` | Canonical authorization | Action and benchmark contracts |
| `internal/calibration` | Trusted routing-objective derivation | Action contract |
| `internal/learning` | Portable outcome inference | Admitted action and typed state contracts |
| `internal/router` | Baseline and learned selection | Action, calibration output, learning predictions |
| `internal/executor` | State mutation and transition validation | Action and benchmark contracts |
| `internal/eventlog` | Lifecycle and evidence integrity | Event contract only |
| `internal/control` | Stage orchestration | All runtime boundaries above |
| `cmd/*` | Configuration and dependency wiring | Public internal package APIs |
| `benchmarking/*` | Offline studies and artifact production | Published contracts; never runtime authority |

The central rule is that lower boundaries must not import their callers. In
particular, policy cannot import routing or learning, and executors cannot import
control. `tools/check_architecture.py` enforces the most security-relevant edges
using Go package metadata.

Run it directly or as part of `make check`:

```bash
make architecture-check
```
