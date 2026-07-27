"""Offline policy evaluation with explicit support and stability gates.

This module is deliberately outside the authorization path. It consumes logged
behavior probabilities; it cannot add permissions or select a live action.
"""

from __future__ import annotations

import argparse
import json
import math
import random
from collections.abc import Iterable
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any


@dataclass(frozen=True)
class Observation:
    reward: float
    behavior_probability: float
    target_probability: float
    outcome_prediction_taken: float
    outcome_prediction_target: float
    support_complete: bool


@dataclass(frozen=True)
class Interval:
    estimate: float
    lower: float
    upper: float


@dataclass(frozen=True)
class Diagnostics:
    observations: int
    effective_sample_size: float
    effective_sample_fraction: float
    minimum_behavior_probability: float
    maximum_importance_weight: float
    support_complete: bool
    estimator_range: float


def parse_observation(value: object, line: int) -> Observation:
    if not isinstance(value, dict):
        raise ValueError(f"line {line}: observation must be an object")
    expected = {
        "reward",
        "behavior_probability",
        "target_probability",
        "outcome_prediction_taken",
        "outcome_prediction_target",
        "support_complete",
    }
    if set(value) != expected:
        raise ValueError(f"line {line}: fields must be exactly {sorted(expected)}")
    numeric: dict[str, float] = {}
    for field in expected - {"support_complete"}:
        raw = value[field]
        if isinstance(raw, bool) or not isinstance(raw, int | float):
            raise ValueError(f"line {line}: {field} must be numeric")
        numeric[field] = float(raw)
        if not math.isfinite(numeric[field]):
            raise ValueError(f"line {line}: {field} must be finite")
    if not 0 <= numeric["reward"] <= 1:
        raise ValueError(f"line {line}: reward must be in [0,1]")
    if not 0 < numeric["behavior_probability"] <= 1:
        raise ValueError(f"line {line}: behavior_probability must be in (0,1]")
    if not 0 <= numeric["target_probability"] <= 1:
        raise ValueError(f"line {line}: target_probability must be in [0,1]")
    for field in ("outcome_prediction_taken", "outcome_prediction_target"):
        if not 0 <= numeric[field] <= 1:
            raise ValueError(f"line {line}: {field} must be in [0,1]")
    support = value["support_complete"]
    if not isinstance(support, bool):
        raise ValueError(f"line {line}: support_complete must be boolean")
    return Observation(support_complete=support, **numeric)


def read_jsonl(path: Path) -> list[Observation]:
    observations: list[Observation] = []
    with path.open(encoding="utf-8") as handle:
        for line_number, line in enumerate(handle, start=1):
            if not line.strip():
                continue
            observations.append(parse_observation(json.loads(line), line_number))
    if not observations:
        raise ValueError("offline evaluation requires at least one observation")
    return observations


def point_estimates(
    observations: Iterable[Observation],
    clip: float,
) -> dict[str, float]:
    rows = list(observations)
    if not rows:
        raise ValueError("offline evaluation requires at least one observation")
    if not math.isfinite(clip) or clip <= 0:
        raise ValueError("importance-weight clip must be positive and finite")
    weights = [row.target_probability / row.behavior_probability for row in rows]
    ips = sum(weight * row.reward for weight, row in zip(weights, rows, strict=True)) / len(rows)
    weight_sum = sum(weights)
    snips = (
        sum(weight * row.reward for weight, row in zip(weights, rows, strict=True)) / weight_sum
        if weight_sum > 0
        else math.nan
    )
    clipped = sum(
        min(weight, clip) * row.reward for weight, row in zip(weights, rows, strict=True)
    ) / len(rows)
    doubly_robust = sum(
        row.outcome_prediction_target + weight * (row.reward - row.outcome_prediction_taken)
        for weight, row in zip(weights, rows, strict=True)
    ) / len(rows)
    return {
        "ips": ips,
        "self_normalized_ips": snips,
        "clipped_ips": clipped,
        "doubly_robust": doubly_robust,
    }


def evaluate(
    observations: list[Observation],
    *,
    clip: float = 20,
    bootstrap_samples: int = 1_000,
    seed: int = 42,
    confidence: float = 0.95,
    minimum_ess_fraction: float = 0.25,
    maximum_weight: float = 20,
    maximum_estimator_range: float = 0.1,
) -> dict[str, Any]:
    if bootstrap_samples < 100:
        raise ValueError("bootstrap_samples must be at least 100")
    if not 0 < confidence < 1:
        raise ValueError("confidence must be in (0,1)")
    estimates = point_estimates(observations, clip)
    finite_estimates = [value for value in estimates.values() if math.isfinite(value)]
    estimator_range = max(finite_estimates) - min(finite_estimates)
    weights = [row.target_probability / row.behavior_probability for row in observations]
    weight_sum = sum(weights)
    squared_weight_sum = sum(weight * weight for weight in weights)
    ess = weight_sum * weight_sum / squared_weight_sum if squared_weight_sum > 0 else 0
    diagnostics = Diagnostics(
        observations=len(observations),
        effective_sample_size=ess,
        effective_sample_fraction=ess / len(observations),
        minimum_behavior_probability=min(row.behavior_probability for row in observations),
        maximum_importance_weight=max(weights),
        support_complete=all(row.support_complete for row in observations),
        estimator_range=estimator_range,
    )
    failures: list[str] = []
    if not diagnostics.support_complete:
        failures.append("target policy support is incomplete")
    if diagnostics.effective_sample_fraction < minimum_ess_fraction:
        failures.append("effective sample-size fraction is below the frozen threshold")
    if diagnostics.maximum_importance_weight > maximum_weight:
        failures.append("maximum importance weight exceeds the frozen threshold")
    if diagnostics.estimator_range > maximum_estimator_range:
        failures.append("offline estimators disagree beyond the frozen threshold")

    randomizer = random.Random(seed)
    distributions: dict[str, list[float]] = {name: [] for name in estimates}
    for _ in range(bootstrap_samples):
        sample = [observations[randomizer.randrange(len(observations))] for _ in observations]
        sample_estimates = point_estimates(sample, clip)
        for name, value in sample_estimates.items():
            if math.isfinite(value):
                distributions[name].append(value)
    alpha = (1 - confidence) / 2
    intervals: dict[str, dict[str, float]] = {}
    for name, estimate in estimates.items():
        values = sorted(distributions[name])
        if not values:
            lower = upper = math.nan
        else:
            lower = percentile(values, alpha)
            upper = percentile(values, 1 - alpha)
        intervals[name] = asdict(Interval(estimate=estimate, lower=lower, upper=upper))
    return {
        "schema_version": "0.1.0",
        "admissible": not failures,
        "admission_failures": failures,
        "diagnostics": asdict(diagnostics),
        "estimates": intervals,
        "configuration": {
            "clip": clip,
            "bootstrap_samples": bootstrap_samples,
            "seed": seed,
            "confidence": confidence,
            "minimum_ess_fraction": minimum_ess_fraction,
            "maximum_weight": maximum_weight,
            "maximum_estimator_range": maximum_estimator_range,
        },
    }


def percentile(sorted_values: list[float], probability: float) -> float:
    if len(sorted_values) == 1:
        return sorted_values[0]
    position = probability * (len(sorted_values) - 1)
    lower = math.floor(position)
    upper = math.ceil(position)
    fraction = position - lower
    return sorted_values[lower] * (1 - fraction) + sorted_values[upper] * fraction


def write_exclusive(path: Path, document: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("x", encoding="utf-8") as handle:
        json.dump(document, handle, indent=2, sort_keys=True, allow_nan=False)
        handle.write("\n")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True, help="JSONL observations")
    parser.add_argument("--output", type=Path, required=True, help="new result JSON")
    parser.add_argument("--bootstrap-samples", type=int, default=1_000)
    parser.add_argument("--seed", type=int, default=42)
    parser.add_argument("--clip", type=float, default=20)
    args = parser.parse_args()
    result = evaluate(
        read_jsonl(args.input),
        clip=args.clip,
        bootstrap_samples=args.bootstrap_samples,
        seed=args.seed,
    )
    write_exclusive(args.output, result)


if __name__ == "__main__":
    main()
