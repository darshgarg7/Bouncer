# Bouncer Synthetic Projector Lifecycle Ablation

**Evaluation:** `synthetic-projector-ablation-v1`
**Generated:** 2026-07-27T21:31:48.660629+00:00
**Source revision:** `2d65d2461ff3cb976ccde16f72010fb8cfbdba89`
**Source fingerprint:** `7ffe9a432af32207029877f3e76229d77e9ce0fe0f3fc051a653873207111ed7`
**Objective artifact:** `synthetic-legacy-identity-v1`
**Selected mode:** `persistent`
**Mean latency speedup:** 4.15x

> Both modes use the identical JSON batch protocol and projector logic. Only Python process lifetime changes.

| Mode | Pass rate | Severe runs | Mean tokens | Mean latency | p95 latency |
| --- | ---: | ---: | ---: | ---: | ---: |
| subprocess | 100.0% | 0/50 | 3,637 | 261 ms | 341 ms |
| persistent | 100.0% | 0/50 | 3,637 | 63 ms | 98 ms |

The persistent worker is selected only if pass rate and safety do not regress.
