# Bouncer Synthetic Proposal Ablation

**Evaluation:** `synthetic-proposal-ablation-v1`
**Generated:** 2026-07-27T06:20:14.911120+00:00
**Source revision:** `35c799ca29ddbf4ba710b2bab41b5f3dcc0d6766`
**Source fingerprint:** `e768534e61b175933efa48274f53a7fa5e111c97b206894f8a673cdd6f012e25`
**Objective artifact:** `synthetic-legacy-identity-v1`
**Runs:** 250
**Selected configuration:** `1x3`

> This is a deterministic simulator ablation. It measures architectural cost and fixture safety, not real-model diversity or production safety.

## Results

| Configuration | Pass rate | Severe runs | Mean tokens / success | Token delta vs 3x5 | Mean calls | Mean candidates | Mean latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1x3 | 100.0% | 0/50 | 3,637 | -71.1% | 4.30 | 12.90 | 261 ms |
| 1x5 | 100.0% | 0/50 | 4,194 | -66.7% | 4.30 | 21.50 | 264 ms |
| 2x3 | 100.0% | 0/50 | 7,275 | -42.2% | 8.60 | 25.80 | 269 ms |
| 2x5 | 100.0% | 0/50 | 8,391 | -33.4% | 8.60 | 43.00 | 274 ms |
| 3x5 | 100.0% | 0/50 | 12,592 | +0.0% | 12.90 | 64.50 | 273 ms |

## Selection rule

Choose the lowest-token configuration whose pass rate is within the pre-specified non-inferiority margin and whose severe-run rate is no worse than the 3x5 reference.

The chosen configuration must be revalidated against the real provider; simulator candidate diversity is deterministic.
