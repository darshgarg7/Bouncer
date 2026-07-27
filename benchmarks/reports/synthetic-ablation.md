# Bouncer Synthetic Proposal Ablation

**Evaluation:** `synthetic-proposal-ablation-v1`
**Generated:** 2026-07-20T22:20:04.355996+00:00
**Runs:** 250
**Selected configuration:** `1x3`

> This is a deterministic simulator ablation. It measures architectural cost and fixture safety, not real-model diversity or production safety.

## Results

| Configuration | Pass rate | Severe runs | Mean tokens / success | Token delta vs 3x5 | Mean calls | Mean candidates | Mean latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1x3 | 100.0% | 0/50 | 2,624 | -72.5% | 4.30 | 12.90 | 255 ms |
| 1x5 | 100.0% | 0/50 | 3,182 | -66.7% | 4.30 | 21.50 | 254 ms |
| 2x3 | 100.0% | 0/50 | 5,250 | -45.1% | 8.60 | 25.80 | 259 ms |
| 2x5 | 100.0% | 0/50 | 6,366 | -33.4% | 8.60 | 43.00 | 276 ms |
| 3x5 | 100.0% | 0/50 | 9,555 | +0.0% | 12.90 | 64.50 | 278 ms |

## Selection rule

Choose the lowest-token configuration whose pass rate is within the preregistered non-inferiority margin and whose severe-run rate is no worse than the 3x5 reference.

The chosen configuration must be revalidated against the real provider; simulator candidate diversity is deterministic.
