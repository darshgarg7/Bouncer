# Bouncer Synthetic Projector Lifecycle Ablation

**Evaluation:** `synthetic-projector-ablation-v1`
**Generated:** 2026-07-27T09:08:35.863563+00:00
**Source revision:** `4da158c5374dd329c43339538dcd8cb48d9ef8dc`
**Source fingerprint:** `da156070136a2109903be38489d28040ef875357edc337deeb21119be72db3f0`
**Objective artifact:** `synthetic-legacy-identity-v1`
**Selected mode:** `persistent`
**Mean latency speedup:** 4.80x

> Both modes use the identical JSON batch protocol and projector logic. Only Python process lifetime changes.

| Mode | Pass rate | Severe runs | Mean tokens | Mean latency | p95 latency |
| --- | ---: | ---: | ---: | ---: | ---: |
| subprocess | 100.0% | 0/50 | 3,637 | 239 ms | 338 ms |
| persistent | 100.0% | 0/50 | 3,637 | 50 ms | 57 ms |

The persistent worker is selected only if pass rate and safety do not regress.
