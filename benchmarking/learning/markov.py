"""Dirichlet-smoothed workflow transition priors."""

from __future__ import annotations

import argparse
import json
import math
from collections import Counter, defaultdict
from collections.abc import Iterable, Mapping, Sequence
from pathlib import Path
from typing import Any

from .features import OPERATION_CLASSES


def fit_transition_prior(
    trajectories: Iterable[Sequence[str]], *, alpha: float = 1.0
) -> dict[str, Any]:
    """Fit a first-order transition matrix with symmetric Dirichlet smoothing."""
    if not math.isfinite(alpha) or alpha <= 0:
        raise ValueError("alpha must be positive and finite")
    counts: dict[str, Counter[str]] = defaultdict(Counter)
    observed_rows = 0
    support_operations = (*OPERATION_CLASSES, "END")
    for trajectory in trajectories:
        previous = "START"
        for operation in trajectory:
            if operation not in OPERATION_CLASSES:
                raise ValueError(f"unsupported operation {operation!r}")
            counts[previous][operation] += 1
            previous = operation
            observed_rows += 1
        counts[previous]["END"] += 1
        observed_rows += 1
    if observed_rows == 0:
        raise ValueError("transition fitting requires at least one action")
    support = len(support_operations)
    probabilities: dict[str, dict[str, float]] = {}
    for previous, row in sorted(counts.items()):
        denominator = sum(row.values()) + alpha * support
        probabilities[previous] = {
            operation: (row[operation] + alpha) / denominator for operation in support_operations
        }
    fallback = alpha / (observed_rows + alpha * support)
    return {
        "fallback_probability": fallback,
        "probabilities": probabilities,
    }


def sequence_log_likelihood(operations: Sequence[str], prior: Mapping[str, Any]) -> float:
    """Score a sequence using a fitted transition prior."""
    fallback_raw = prior.get("fallback_probability")
    probabilities = prior.get("probabilities")
    if not isinstance(fallback_raw, int | float) or not isinstance(probabilities, Mapping):
        raise ValueError("malformed transition prior")
    fallback = float(fallback_raw)
    previous = "START"
    total = 0.0
    for operation in operations:
        row = probabilities.get(previous, {})
        probability = row.get(operation, fallback) if isinstance(row, Mapping) else fallback
        total += math.log(float(probability))
        previous = operation
    row = probabilities.get(previous, {})
    probability = row.get("END", fallback) if isinstance(row, Mapping) else fallback
    total += math.log(float(probability))
    return total


def main() -> None:
    """Fit a transition prior from JSONL operation sequences."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--alpha", type=float, default=1.0)
    args = parser.parse_args()
    trajectories: list[list[str]] = []
    with args.input.open(encoding="utf-8") as handle:
        for line_number, line in enumerate(handle, start=1):
            if not line.strip():
                continue
            value = json.loads(line)
            if not isinstance(value, list) or any(not isinstance(item, str) for item in value):
                raise ValueError(f"line {line_number}: expected an array of operation strings")
            trajectories.append(value)
    artifact = fit_transition_prior(trajectories, alpha=args.alpha)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("x", encoding="utf-8") as handle:
        json.dump(artifact, handle, indent=2, sort_keys=True, allow_nan=False)
        handle.write("\n")


if __name__ == "__main__":
    main()
