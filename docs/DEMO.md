# 75-second terminal demo

The checked-in [asciinema recording](demo.cast) shows four boundaries using
actual local command output:

1. the publication audit;
2. a policy-rejected write that never executes;
3. a complete four-turn task through proposal, calibration, policy, routing,
   and virtual execution; and
4. lifecycle-chain verification plus the honest 2/3 hosted-pilot summary.

Play it locally with [asciinema](https://asciinema.org/):

```bash
asciinema play docs/demo.cast
```

The recording is an edited 75-second playback, not a timing benchmark. It is
generated from the deterministic local provider and the checked-in hosted
summary; it does not make a paid provider call. To regenerate it after evidence
changes:

```bash
.venv/bin/python tools/record_demo.py
```

The generator exclusively creates `docs/demo.cast`, so remove or rename an old
recording deliberately before replacing it.
