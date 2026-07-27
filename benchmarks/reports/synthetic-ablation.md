# Bouncer Synthetic Proposal Ablation

**Evaluation:** `synthetic-proposal-ablation-v1`
**Generated:** 2026-07-27T09:08:17.130717+00:00
**Source revision:** `4da158c5374dd329c43339538dcd8cb48d9ef8dc`
**Source fingerprint:** `da156070136a2109903be38489d28040ef875357edc337deeb21119be72db3f0`
**Objective artifact:** `synthetic-legacy-identity-v1`
**Runs:** 250
**Selected configuration:** `1x3`

> This is a deterministic simulator ablation. It measures architectural cost and fixture safety, not real-model diversity or production safety.

## Results

| Configuration | Pass rate | Severe runs | Mean tokens / success | Token delta vs 3x5 | Mean calls | Mean candidates | Mean latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1x3 | 100.0% | 0/50 | 3,637 | -71.1% | 4.30 | 12.90 | 241 ms |
| 1x5 | 100.0% | 0/50 | 4,194 | -66.7% | 4.30 | 21.50 | 237 ms |
| 2x3 | 100.0% | 0/50 | 7,275 | -42.2% | 8.60 | 25.80 | 246 ms |
| 2x5 | 100.0% | 0/50 | 8,391 | -33.4% | 8.60 | 43.00 | 257 ms |
| 3x5 | 100.0% | 0/50 | 12,592 | +0.0% | 12.90 | 64.50 | 250 ms |

## Selection rule

Choose the lowest-token configuration whose pass rate is within the pre-specified non-inferiority margin and whose severe-run rate is no worse than the 3x5 reference.

The chosen configuration must be revalidated against the real provider; simulator candidate diversity is deterministic.
