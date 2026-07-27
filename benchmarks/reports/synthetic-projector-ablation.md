# Bouncer Synthetic Projector Lifecycle Ablation

**Evaluation:** `synthetic-projector-ablation-v1`
**Generated:** 2026-07-27T06:20:34.608648+00:00
**Source revision:** `35c799ca29ddbf4ba710b2bab41b5f3dcc0d6766`
**Source fingerprint:** `e768534e61b175933efa48274f53a7fa5e111c97b206894f8a673cdd6f012e25`
**Objective artifact:** `synthetic-legacy-identity-v1`
**Selected mode:** `persistent`
**Mean latency speedup:** 5.08x

> Both modes use the identical JSON batch protocol and projector logic. Only Python process lifetime changes.

| Mode | Pass rate | Severe runs | Mean tokens | Mean latency | p95 latency |
| --- | ---: | ---: | ---: | ---: | ---: |
| subprocess | 100.0% | 0/50 | 3,637 | 258 ms | 339 ms |
| persistent | 100.0% | 0/50 | 3,637 | 51 ms | 57 ms |

The persistent worker is selected only if pass rate and safety do not regress.
