# Build and operating guide

## Requirements

- Go 1.23 or newer
- Python 3.11 or newer
- Python packages from `.[dev]`
- Optional OpenAI-compatible NVIDIA NIM endpoint

Create an isolated Python environment if the dependencies are not already available:

```bash
make bootstrap
```

The Makefile automatically uses `.venv` after bootstrapping. To use a different
location, run `make bootstrap VENV=/absolute/path` and pass the same `VENV`
value to later Make commands.

## Quality gates

```bash
make check
```

`make check` validates all frozen JSON fixtures, runs Go tests under the race detector, runs Python unit and integration tests, enforces Go and Python formatting, runs Go vet, Ruff, and strict mypy, and builds all five binaries. `make coverage` enforces the current ratchet; `make fuzz-smoke` runs bounded decoder and router fuzzing.

## Build

```bash
make build
```

Outputs:

- `bin/bouncer-harness`
- `bin/bouncer-run`
- `bin/bouncer-provider-gate`
- `bin/bouncer-sandbox`
- `bin/bouncer-verify-log`

The `bin/` directory is ignored by version control.

## Connectivity spike

Edit a copy of the example manifest to point at the target endpoint. Do not store credentials in the manifest.

```bash
cp configs/run-manifest.example.json configs/run-manifest.local.json
export NIM_API_KEY="..."
bin/bouncer-harness \
  -manifest configs/run-manifest.local.json \
  -task benchmarks/tasks/task-001.json
```

The harness creates an append-only JSONL file under `benchmarks/results/` unless `-output` is supplied.

### NVIDIA hosted API

The checked-in NVIDIA manifest freezes the hosted endpoint and generation
settings used for qualification. Keep only the credential in `.env`; settings
inside `.env` are intentionally not applied because invisible overrides would
make run hashes misleading. The field names and hosted endpoint follow
[NVIDIA's model-specific API reference](https://docs.api.nvidia.com/nim/reference/nvidia-nemotron-3-ultra-550b-a55b-infer).

```bash
cp .env.example .env
chmod 600 .env
# Set NVIDIA_API_KEY in .env, then load it into this shell.
set -a
. ./.env
set +a

make build
bin/bouncer-run \
  -manifest configs/run-manifest.nvidia-hosted.json \
  -task benchmarks/tasks/task-001.json \
  -event-log benchmarks/results/nvidia-pilot-events.jsonl \
  -output benchmarks/results/nvidia-pilot-result.json
```

To reproduce the three-task published smoke pilot with the virtual executor,
run `tools/run_nvidia_pilot.sh`. It creates a new timestamped directory under
`benchmarks/results/`, runs tasks 001–003 sequentially, and verifies every event
chain. Pass an unused output directory as its first argument to override the
default.

`NVIDIA_API_KEY` and the older `NIM_API_KEY` name are both accepted, with
`NIM_API_KEY` taking precedence when both are present. The hosted manifest uses
NVIDIA's current `reasoning_budget`, `reasoning_effort`, and `top_p` request
fields. `NVIDIA_PROFILE` is not an API request field and is not forwarded.

For a self-hosted Nemotron 3 Ultra NIM, start the server with the `nemotron_v3` reasoning parser and freeze the container version in the benchmark record. The example manifest sends `chat_template_kwargs: {"enable_thinking": true}` with `thinking_token_budget`; `max_tokens` remains the total generation ceiling. Hosted endpoints that expose `reasoning_budget` must set `model.reasoning_budget_parameter` to that name in the copied manifest. Without the reasoning parser, reasoning markup may remain in assistant content and will correctly fail the strict JSON-beam decoder. See NVIDIA's [Nemotron 3 Ultra deployment guide](https://docs.nvidia.com/nim/large-language-models/latest/day-0/get-started-nemotron-3-ultra.html#control-thinking-budget).

## Complete static run

```bash
bin/bouncer-run \
  -manifest configs/run-manifest.local.json \
  -task benchmarks/tasks/task-001.json \
  -project-root "$PWD" \
  -seed 42
```

`-project-root` must contain `configs/skill_dag.json`. The default Go policy engine loads the DAG directly; Python modes are retained for differential reference and historical reproduction.

The measured lower-overhead default is one proposer returning one action:

```bash
bin/bouncer-run \
  -manifest configs/run-manifest.local.json \
  -task benchmarks/tasks/task-001.json \
  -project-root "$PWD" \
  -event-log benchmarks/results/task-001-events.jsonl \
  -output benchmarks/results/task-001.json
```

Verify the completed chain with `bin/bouncer-verify-log -event-log benchmarks/results/task-001-events.jsonl`. The historical 3×5 report uses `configs/run-manifest.synthetic-v1.json`; it is not the runtime default.

## Provider connectivity gate

```bash
bin/bouncer-provider-gate \
  -manifest configs/run-manifest.local.json \
  -task benchmarks/tasks/task-001.json \
  -batches 100 \
  -output-dir benchmarks/results/provider-gate-real
```

The directory must not already exist. It contains all batch records and a summary with manifest and task hashes.

## Resumable provider evaluation

```bash
python3 -m benchmarking.provider_evaluate \
  --run-manifest configs/run-manifest.local.json \
  --output-dir benchmarks/results/provider-evaluation-real
```

After interruption, run the same command with `--resume`. Existing records are accepted only when the frozen configuration fingerprint matches.

## Remote sandbox boundary

The checked-in service defaults to the reversible virtual executor. It authenticates requests, durably replays idempotent responses across restarts, and validates remote transitions. On Linux, `-backend rooted -workspace-root /absolute/root` enables the narrow `openat2` filesystem broker; this still requires the gVisor boundary and external qualification before real authorization.

```bash
export BOUNCER_SANDBOX_TOKEN="local-development-token"
bin/bouncer-sandbox \
  -listen 127.0.0.1:8082 \
  -idempotency-dir data/sandbox-idempotency
```

For an explicitly local HTTP test:

```bash
bin/bouncer-run \
  -endpoint http://127.0.0.1:8001/v1 \
  -task benchmarks/tasks/task-001.json \
  -executor-mode remote \
  -sandbox-url http://127.0.0.1:8082 \
  -allow-insecure-sandbox \
  -policy-engine go
```

Remote execution requires HTTPS unless `-allow-insecure-sandbox` is explicitly supplied.

## Local simulator

Run the deterministic NIM-compatible endpoint:

```bash
python3 -m benchmarking.mock_nim_cli --port 8001
```

In a second terminal:

```bash
bin/bouncer-run \
  -endpoint http://127.0.0.1:8001/v1 \
  -task benchmarks/tasks/task-001.json \
  -seed 2
```

## Full evaluation

```bash
make evaluate-synthetic
make evaluate-ablation
make evaluate-projector
make evaluate-mechanisms
make evaluate-ope-simulation
```

Outputs:

- `benchmarks/reports/synthetic-mvb.md`
- `benchmarks/reports/synthetic-mvb-results.json`
- `benchmarks/reports/synthetic-ablation.md`
- `benchmarks/reports/synthetic-projector-ablation.md`
- `benchmarks/reports/mechanism.md`

## Containers

```bash
make containers
```

`Dockerfile.bouncer` packages the control plane and independent Python reference. `Dockerfile.sandbox` produces a non-root distroless sandbox image. The gVisor-oriented template is under `deploy/kubernetes`. See [the release checklist](RELEASE.md) before publishing images.

## Configuration

| Setting | Location | Default |
| --- | --- | --- |
| Model ID | run manifest | `nvidia/nemotron-3-ultra-550b-a55b` |
| Endpoint | run manifest or `-endpoint` | `http://localhost:8000/v1` |
| API key | `NIM_API_KEY` | unset |
| Proposers | run manifest | 1; ensemble modes are explicit |
| Beam width | run manifest | 1; wider beams are explicit |
| Reasoning budget | run manifest | 1024 |
| Total generation limit | run manifest | 1536 |
| Proposal timeout | run manifest | 120 seconds |
| Maximum task turns | run manifest | 8 |

## Troubleshooting

### `finish_reason: length`

The output is classified as truncated and rejected before JSON parsing. Increase the total generation limit or reduce the beam schema, then create a new benchmark manifest version before comparing runs.

### Empty final content

Reasoning may have consumed the generation budget. Confirm that `--reasoning-parser nemotron_v3` is enabled, then inspect `completion_tokens_details.reasoning_tokens` and the total generation ceiling.

### Projector module not found

Set `-project-root` to the repository root and verify `python3 -m constraint_projection --help` works there.

### No valid candidate

Inspect `constraint_feedback` and the per-action projection trace. The loop replans; it never falls back to executing a rejected action.

### HTTP 429 or 5xx

The client retries with bounded jitter. Examine attempt counts and server rate-limit policy before increasing concurrency.

### Local latency is dominated by projection

Use the default `-policy-engine go`. Python persistent mode exists only for differential reference and the historical lifecycle ablation.
