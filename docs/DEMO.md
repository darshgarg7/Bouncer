# Demo

## Primary live demo

Run five control-boundary checks with no credentials, network, Docker, Python
environment, or external provider:

```bash
make demo
```

It exercises actual Go packages for:

1. strict malformed-proposal rejection;
2. deterministic policy rejection of an out-of-root write;
3. successful virtual execution of an admitted write;
4. verification of a complete event chain and rejection of a modified event;
5. learned-routing inference in shadow mode while baseline execution remains
   authoritative.

Expected output:

```text
Bouncer live demo — no credentials, network, or Docker
[PASS] 1/5 malformed proposal rejected: ... unknown field "unexpected"
[PASS] 2/5 policy rejected dangerous action: TARGET_OUTSIDE_ALLOWED_ROOT
         audit: action=dangerous-write allowed=false executed=false
[PASS] 3/5 safe action executed: modified=workspace/demo/config.yaml content="enabled: true"
[PASS] 4/5 tamper detected: event line 3: content hash mismatch
[PASS] 5/5 learned routing shadowed: baseline=safe-write shadow=safe-read executed=safe-write
DEMO PASSED: 5/5 control boundaries behaved as expected
```

The test `cmd/bouncer-demo/main_test.go` asserts all five behaviors, and CI runs
`make demo` on both Linux and macOS.

## Multi-process Docker demonstration

Run the OpenAI-compatible deterministic provider and Bouncer as separate
containers:

```bash
docker compose -f compose.demo.yaml up --build --abort-on-container-exit --exit-code-from bouncer
```

This path performs one complete task in learned-routing shadow mode. It uses no
credentials or paid endpoint. Compose exits with Bouncer's status.

## Visual assets

The README embeds [bouncer-demo.gif](assets/bouncer-demo.gif), which is rendered
from the live command output:

```bash
make demo-gif
```

The older [75-second asciinema recording](demo.cast) is retained as a historical
edited playback. It is not the primary live demo and is not a timing benchmark.
