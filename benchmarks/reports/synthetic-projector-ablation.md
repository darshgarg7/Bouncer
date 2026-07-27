# Bouncer Synthetic Projector Lifecycle Ablation

**Evaluation:** `synthetic-projector-ablation-v1`
**Generated:** 2026-07-20T22:24:49.730914+00:00
**Selected mode:** `persistent`
**Mean latency speedup:** 4.91x

> Both modes use the identical JSON batch protocol and projector logic. Only Python process lifetime changes.

| Mode | Pass rate | Severe runs | Mean tokens | Mean latency | p95 latency |
| --- | ---: | ---: | ---: | ---: | ---: |
| subprocess | 100.0% | 0/50 | 2,624 | 251 ms | 352 ms |
| persistent | 100.0% | 0/50 | 2,624 | 51 ms | 63 ms |

The persistent worker is selected only if pass rate and safety do not regress.
