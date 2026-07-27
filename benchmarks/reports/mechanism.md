# Bouncer controlled mechanism study

**Evaluation:** `synthetic-mechanism-v1`
**Generated:** 2026-07-26T20:21:54.106070+00:00
**Wall time:** 10.989 seconds

> This is deterministic smoke-suite evidence with synthetic provider telemetry. It measures implementation behavior, not real-model capability, production safety, or causal effects.

| Condition | Pass rate | Severe runs | Mean tokens/success | Mean calls | Mean candidates | Token delta vs single+policy |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| single_policy | 100.0% | 0/50 | 2,169 | 4.62 | 4.62 | +0 |
| beam_first_valid | 100.0% | 0/50 | 3,182 | 4.30 | 21.50 | +1,013 |
| scalar_utility | 100.0% | 0/50 | 3,182 | 4.30 | 21.50 | +1,013 |
| random_safe | 50.0% | 0/50 | 3,279 | 5.78 | 28.90 | +2,096 |
| pareto_utility | 100.0% | 0/50 | 3,182 | 4.30 | 21.50 | +1,013 |
| epsilon_pareto | 100.0% | 0/50 | 3,182 | 4.30 | 21.50 | +1,013 |
| fixed_3x3 | 100.0% | 0/50 | 7,878 | 12.90 | 38.70 | +5,708 |
| adaptive_1_to_3x3 | 100.0% | 0/50 | 2,626 | 4.30 | 12.90 | +456 |

The deterministic policy is identical across conditions. This isolates proposal width, routing semantics, exploration, and adaptive expansion from the unsafe unfiltered baseline in the historical integration study.

Raw paired records are in [`mechanism-results.json`](mechanism-results.json).
