# Bouncer Synthetic Proposal Ablation

**Evaluation:** `synthetic-proposal-ablation-v1`
**Generated:** 2026-07-27T18:54:24.604998+00:00
**Source revision:** `76d2508b1497830522233a35b044385d84534729`
**Source fingerprint:** `8b10126665a1a4dead73f91bab490ca63b02e2ecace54ec2b5551105c6fce65d`
**Objective artifact:** `synthetic-legacy-identity-v1`
**Runs:** 250
**Selected configuration:** `1x3`

> This is a deterministic simulator ablation. It measures architectural cost and fixture safety, not real-model diversity or production safety.

## Results

| Configuration | Pass rate | Severe runs | Mean tokens / success | Token delta vs 3x5 | Mean calls | Mean candidates | Mean latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1x3 | 100.0% | 0/50 | 3,637 | -71.1% | 4.30 | 12.90 | 292 ms |
| 1x5 | 100.0% | 0/50 | 4,194 | -66.7% | 4.30 | 21.50 | 273 ms |
| 2x3 | 100.0% | 0/50 | 7,275 | -42.2% | 8.60 | 25.80 | 241 ms |
| 2x5 | 100.0% | 0/50 | 8,391 | -33.4% | 8.60 | 43.00 | 241 ms |
| 3x5 | 100.0% | 0/50 | 12,592 | +0.0% | 12.90 | 64.50 | 264 ms |

## Selection rule

Choose the lowest-token configuration whose pass rate is within the pre-specified non-inferiority margin and whose severe-run rate is no worse than the 3x5 reference.

The chosen configuration must be revalidated against the real provider; simulator candidate diversity is deterministic.
