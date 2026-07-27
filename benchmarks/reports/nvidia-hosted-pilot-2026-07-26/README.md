# NVIDIA hosted provider smoke pilot — 2026-07-26

This is a small, credentialed development pilot of the complete Bouncer control
loop against NVIDIA's hosted API. It establishes that a real hosted model can
propose strictly parsed actions, receive deterministic policy feedback, and
complete the three selected virtual tasks under the checked-in transition
contract. It is not a model comparison, provider qualification, sandbox test,
or effectiveness claim beyond these fixtures.

## Frozen setup

- Source revision: `f76ab4c0e275d4f94ac78f82e13fb9f3c935344d`
- UTC interval: 2026-07-27 04:21:19–04:26:07
- Model: `nvidia/nemotron-3-ultra-550b-a55b`
- Endpoint: `https://integrate.api.nvidia.com/v1`
- Manifest: [`configs/run-manifest.nvidia-hosted.json`](../../../configs/run-manifest.nvidia-hosted.json)
- Manifest SHA-256: `6b712204f294623056f40c374f3665e3e272d2eca842efd255ab44b5c3473e11`
- Configuration: one proposer, beam width one, seed 42, lexicographic routing,
  canonical Go policy, virtual executor, maximum eight turns

The three tasks were selected before the run by the fixed pilot script. They
edit a timeout, add a health route, and repair JSON. All operate on authored
virtual state; none touches the checkout or a host filesystem.

## Results

| Task | Oracle | Turns | Policy rejections | Executed actions | Parsed-response tokens | Duration |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `task-001` | pass | 4 | 0 | 4 | 3,836 | 27.982 s |
| `task-002` | pass | 6 | 2 | 4 | 5,419 | 99.400 s |
| `task-003` | pass | 5 | 1 | 4 | 5,107 | 160.139 s |
| **Total** | **3/3** | **15** | **3** | **12** | **14,362** | **287.521 s** |

All three runs reached `task.complete`, matched their exact file oracle, and
reported zero severe virtual mutations. Tasks 002 and 003 initially proposed
actions that skipped declared dependencies; the deterministic policy rejected
them, added canonical feedback to state, and the model subsequently proposed
the required operations.

There were 16 HTTP attempts for 15 accepted proposal rounds. One task-001
proposal succeeded on its second attempt. Token totals above are the provider
usage attached to strictly parsed responses; the client cannot attribute tokens to a
failed HTTP or network attempt when the provider returns no usable completion.
The provider returned zero in `completion_tokens_details.reasoning_tokens` for
every accepted response, so this pilot cannot separate reasoning from visible
completion usage.

## Evidence artifacts

Each task has three checked-in files:

- `task-NNN-events.jsonl`: the append-only decision and transition stream;
- `task-NNN-result.json`: the final state, oracle result, aggregate metrics, and
  in-memory trace; and
- `task-NNN-verification.json`: the lifecycle and hash-chain verification
  result, including the terminal chain hash.

The verified terminal hashes are:

| Task | Events | Terminal event | Final hash |
| --- | ---: | --- | --- |
| `task-001` | 18 | `run.completed` | `d9017fedbd1c8fcdab9b353a0ac6de5371e289cf94e64518e91dfc09f926e35f` |
| `task-002` | 22 | `run.completed` | `54d96b101dd5fbce33a0f0a5163fb9eb54c22bf7aac04535833ba080d11e8a45` |
| `task-003` | 20 | `run.completed` | `668fc9e92276ec2bc41f371ef0d2e76b6ac6280aefd54e8f49a53ff7c4488210` |

The Git commit containing this report externally anchors those verification
records. Provider request and response bodies are represented by SHA-256 hashes;
credentials and authorization headers are not recorded.

## Reproduce

Create a local `.env` from `.env.example`, add `NVIDIA_API_KEY`, and run:

```bash
tools/run_nvidia_pilot.sh benchmarks/results/my-nvidia-pilot
```

The script builds the binaries, runs tasks 001–003 sequentially with the virtual
executor, and verifies each completed event log. Outputs use exclusive-create
semantics, so the destination must not already exist.

## Interpretation boundary

Three authored tasks and one model/seed are connectivity and mechanism evidence,
not a statistical benchmark. There is no equal-permission baseline, no paired
interval, no audited external task environment, no real sandbox execution, and
no second model family. The synthetic token-efficiency and safety claims are
therefore not promoted by this pilot.
