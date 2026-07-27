# Bouncer Synthetic Projector Lifecycle Ablation

**Evaluation:** `synthetic-projector-ablation-v1`
**Generated:** 2026-07-27T18:53:34.895498+00:00
**Source revision:** `76d2508b1497830522233a35b044385d84534729`
**Source fingerprint:** `8b10126665a1a4dead73f91bab490ca63b02e2ecace54ec2b5551105c6fce65d`
**Objective artifact:** `synthetic-legacy-identity-v1`
**Selected mode:** `persistent`
**Mean latency speedup:** 4.81x

> Both modes use the identical JSON batch protocol and projector logic. Only Python process lifetime changes.

| Mode | Pass rate | Severe runs | Mean tokens | Mean latency | p95 latency |
| --- | ---: | ---: | ---: | ---: | ---: |
| subprocess | 100.0% | 0/50 | 3,637 | 293 ms | 411 ms |
| persistent | 100.0% | 0/50 | 3,637 | 61 ms | 87 ms |

The persistent worker is selected only if pass rate and safety do not regress.
