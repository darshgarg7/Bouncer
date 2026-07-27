"""Small dependency-free Isolation Forest for offline telemetry monitoring."""

from __future__ import annotations

import argparse
import json
import math
import random
from collections.abc import Mapping, Sequence
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any

FEATURES = (
    "rejection_rate",
    "retry_rate",
    "no_progress_streak",
    "tool_switch_rate",
    "latency_delta_ms",
    "transition_nll",
)


@dataclass
class Node:
    """One isolation tree node."""

    size: int
    feature: int | None = None
    split: float | None = None
    left: Node | None = None
    right: Node | None = None


@dataclass
class IsolationForest:
    """An ensemble of random isolation trees."""

    sample_size: int
    trees: list[Node]

    def score(self, sample: Sequence[float]) -> float:
        """Return the conventional [0,1] anomaly score."""
        if len(sample) != len(FEATURES) or any(not math.isfinite(value) for value in sample):
            raise ValueError("anomaly sample has an invalid feature vector")
        average_path = sum(path_length(tree, sample, 0) for tree in self.trees) / len(self.trees)
        normalizer = average_path_length(self.sample_size)
        return 2 ** (-average_path / normalizer) if normalizer > 0 else 0.0


def fit(
    samples: Sequence[Sequence[float]],
    *,
    trees: int = 100,
    sample_size: int = 128,
    seed: int = 42,
) -> IsolationForest:
    """Fit an Isolation Forest using reproducible subsampling and splits."""
    if not samples or trees < 1 or sample_size < 2:
        raise ValueError("isolation forest requires samples, trees, and sample_size >= 2")
    matrix = [validate_sample(sample) for sample in samples]
    size = min(sample_size, len(matrix))
    maximum_depth = math.ceil(math.log2(size))
    randomizer = random.Random(seed)
    fitted = [
        build_tree(randomizer.sample(matrix, size), 0, maximum_depth, randomizer)
        for _ in range(trees)
    ]
    return IsolationForest(sample_size=size, trees=fitted)


def build_tree(
    samples: Sequence[Sequence[float]],
    depth: int,
    maximum_depth: int,
    randomizer: random.Random,
) -> Node:
    """Recursively create one random isolation tree."""
    if depth >= maximum_depth or len(samples) <= 1:
        return Node(size=len(samples))
    splittable = [
        feature
        for feature in range(len(FEATURES))
        if min(sample[feature] for sample in samples) < max(sample[feature] for sample in samples)
    ]
    if not splittable:
        return Node(size=len(samples))
    feature = randomizer.choice(splittable)
    minimum = min(sample[feature] for sample in samples)
    maximum = max(sample[feature] for sample in samples)
    split = randomizer.uniform(minimum, maximum)
    left = [sample for sample in samples if sample[feature] < split]
    right = [sample for sample in samples if sample[feature] >= split]
    return Node(
        size=len(samples),
        feature=feature,
        split=split,
        left=build_tree(left, depth + 1, maximum_depth, randomizer),
        right=build_tree(right, depth + 1, maximum_depth, randomizer),
    )


def path_length(node: Node, sample: Sequence[float], depth: int) -> float:
    """Measure a sample's path length through one tree."""
    if node.feature is None or node.split is None or node.left is None or node.right is None:
        return depth + average_path_length(node.size)
    branch = node.left if sample[node.feature] < node.split else node.right
    return path_length(branch, sample, depth + 1)


def average_path_length(size: int) -> float:
    """Approximate the unsuccessful-search length of a binary tree."""
    if size <= 1:
        return 0.0
    if size == 2:
        return 1.0
    return 2 * (math.log(size - 1) + 0.5772156649) - 2 * (size - 1) / size


def validate_sample(sample: Sequence[float]) -> list[float]:
    """Validate and copy one telemetry vector."""
    if len(sample) != len(FEATURES):
        raise ValueError("anomaly sample has the wrong feature count")
    values = [float(value) for value in sample]
    if any(not math.isfinite(value) for value in values):
        raise ValueError("anomaly features must be finite")
    return values


def vector(window: Mapping[str, Any]) -> list[float]:
    """Extract the frozen anomaly feature order from a window record."""
    features = window.get("features")
    if not isinstance(features, Mapping):
        raise ValueError("anomaly window features must be an object")
    return validate_sample([float(features[name]) for name in FEATURES])


def main() -> None:
    """Train and serialize an offline shadow Isolation Forest."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--trees", type=int, default=100)
    parser.add_argument("--sample-size", type=int, default=128)
    parser.add_argument("--seed", type=int, default=42)
    args = parser.parse_args()
    windows: list[list[float]] = []
    with args.input.open(encoding="utf-8") as handle:
        for line in handle:
            if line.strip():
                windows.append(vector(json.loads(line)))
    forest = fit(windows, trees=args.trees, sample_size=args.sample_size, seed=args.seed)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("x", encoding="utf-8") as handle:
        json.dump(
            {
                "schema_version": "0.1.0",
                "feature_names": FEATURES,
                "sample_size": forest.sample_size,
                "trees": [asdict(tree) for tree in forest.trees],
            },
            handle,
            indent=2,
            sort_keys=True,
            allow_nan=False,
        )
        handle.write("\n")


if __name__ == "__main__":
    main()
