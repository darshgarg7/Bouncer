# Bouncer controlled mechanism study

**Evaluation:** `synthetic-mechanism-v1`
**Generated:** 2026-07-27T18:53:28.607137+00:00
**Source revision:** `76d2508b1497830522233a35b044385d84534729`
**Source fingerprint:** `8b10126665a1a4dead73f91bab490ca63b02e2ecace54ec2b5551105c6fce65d`
**Objective artifact:** `bootstrap-operation-priors-v1`
**Wall time:** 15.212 seconds

> This is deterministic smoke-suite evidence with synthetic provider telemetry. It measures implementation behavior, not real-model capability, production safety, or causal effects.

| Condition | Pass rate | Severe runs | Mean tokens/success | Mean calls | Mean candidates | Token delta vs single+policy |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| single_policy | 100.0% | 0/50 | 3,257 | 4.62 | 4.62 | +0 |
| beam_first_valid | 100.0% | 0/50 | 4,194 | 4.30 | 21.50 | +938 |
| scalar_utility | 0.0% | 0/50 | not estimable | 8.00 | 40.00 | +4,491 |
| random_safe | 2.0% | 0/50 | 4,941 | 7.84 | 39.20 | +4,356 |
| pareto_utility | 0.0% | 0/50 | not estimable | 8.00 | 40.00 | +4,491 |
| epsilon_pareto | 0.0% | 0/50 | not estimable | 8.00 | 40.00 | +4,491 |
| fixed_3x3 | 100.0% | 0/50 | 10,915 | 12.90 | 38.70 | +7,658 |
| adaptive_1_to_3x3 | 100.0% | 0/50 | 10,915 | 12.90 | 38.70 | +7,658 |

The deterministic policy is identical across conditions. This isolates proposal width, routing semantics, exploration, and adaptive expansion from the unsafe unfiltered baseline in the historical integration study.

Raw paired records are in [`mechanism-results.json`](mechanism-results.json).
