"""Dependency-free Isolation Forest training for portable anomaly artifacts."""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import random
from collections.abc import Mapping, Sequence
from dataclasses import asdict, dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

ARTIFACT_SCHEMA_VERSION = "0.1.0"
FEATURE_SCHEMA_VERSION = "0.1.0"
WINDOW_SCHEMA_VERSION = "0.1.0"
DEFAULT_THRESHOLD = 0.65
MAX_TREES = 4096
MAX_SAMPLE_SIZE = 1_000_000
MAX_TREE_DEPTH = 64
MIN_INT64 = -(2**63)
MAX_INT64 = 2**63 - 1
MIN_ACTIVE_VALIDATION_ROWS = 20
MIN_ACTIVE_CLASS_ROWS = 5
MIN_ACTIVE_TRUE_POSITIVE_RATE = 0.80
MAX_ACTIVE_FALSE_POSITIVE_RATE = 0.05

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
        values = validate_sample(sample)
        if self.sample_size < 2 or not self.trees:
            raise ValueError("isolation forest is empty")
        average_path = sum(path_length(tree, values, 0) for tree in self.trees) / len(self.trees)
        normalizer = average_path_length(self.sample_size)
        return 2 ** (-average_path / normalizer) if normalizer > 0 else 0.0


@dataclass(frozen=True)
class ValidationSummary:
    """Labeled holdout metrics embedded in artifact provenance."""

    dataset_sha256: str
    rows: int
    normal_rows: int
    anomaly_rows: int
    true_positive_rate: float
    false_positive_rate: float

    def passes_active_gate(self) -> bool:
        """Return whether the frozen minimum eligibility gates pass."""
        return (
            self.rows >= MIN_ACTIVE_VALIDATION_ROWS
            and self.normal_rows >= MIN_ACTIVE_CLASS_ROWS
            and self.anomaly_rows >= MIN_ACTIVE_CLASS_ROWS
            and self.normal_rows + self.anomaly_rows == self.rows
            and self.true_positive_rate >= MIN_ACTIVE_TRUE_POSITIVE_RATE
            and self.false_positive_rate <= MAX_ACTIVE_FALSE_POSITIVE_RATE
        )


def fit(
    samples: Sequence[Sequence[float]],
    *,
    trees: int = 100,
    sample_size: int = 128,
    seed: int = 42,
) -> IsolationForest:
    """Fit an Isolation Forest using reproducible subsampling and splits."""
    if len(samples) < 2:
        raise ValueError("isolation forest requires at least two samples")
    if trees < 1 or trees > MAX_TREES:
        raise ValueError(f"trees must be between 1 and {MAX_TREES}")
    if sample_size < 2 or sample_size > MAX_SAMPLE_SIZE:
        raise ValueError(f"sample_size must be between 2 and {MAX_SAMPLE_SIZE}")
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
    split = minimum + randomizer.random() * (maximum - minimum)
    if split <= minimum:
        split = math.nextafter(minimum, maximum)
    left = [sample for sample in samples if sample[feature] < split]
    right = [sample for sample in samples if sample[feature] >= split]
    if not left or not right:
        return Node(size=len(samples))
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
    if any(isinstance(value, bool) or not isinstance(value, (int, float)) for value in sample):
        raise ValueError("anomaly features must be JSON numbers, not booleans")
    values = [float(value) for value in sample]
    if any(not math.isfinite(value) for value in values):
        raise ValueError("anomaly features must be finite")
    return values


def vector(window: Mapping[str, Any]) -> list[float]:
    """Extract and validate the frozen anomaly feature order from a window."""
    features = window.get("features")
    if not isinstance(features, Mapping):
        raise ValueError("anomaly window features must be an object")
    names = set(features)
    expected = set(FEATURES)
    if names != expected:
        missing = sorted(expected - names)
        unexpected = sorted(str(name) for name in names - expected)
        raise ValueError(
            f"anomaly window feature contract mismatch: missing={missing}, unexpected={unexpected}"
        )
    streak = features["no_progress_streak"]
    if isinstance(streak, bool) or not isinstance(streak, int):
        raise ValueError("no_progress_streak must be a non-negative integer")
    try:
        values = validate_sample([features[name] for name in FEATURES])
    except KeyError as error:
        raise ValueError(f"anomaly window is missing feature {error.args[0]!r}") from error
    if not 0 <= values[0] <= 1 or not 0 <= values[1] <= 1 or not 0 <= values[3] <= 1:
        raise ValueError("anomaly rate features must be between 0 and 1")
    if values[2] < 0 or not values[2].is_integer():
        raise ValueError("no_progress_streak must be a non-negative integer")
    if values[5] < 0:
        raise ValueError("transition_nll must be non-negative")
    return values


def validate_window_record(record: Mapping[str, Any], *, labeled: bool) -> None:
    """Enforce the published training or labeled-validation window contract."""
    required = {
        "schema_version",
        "run_id",
        "task_id",
        "turn",
        "features",
        "rule_alerts",
    }
    if labeled:
        required.add("is_anomaly")
    allowed = required | {"anomaly_score"}
    keys = set(record)
    if keys != required and keys != allowed:
        missing = sorted(required - keys)
        unexpected = sorted(str(key) for key in keys - allowed)
        raise ValueError(
            f"anomaly window contract mismatch: missing={missing}, unexpected={unexpected}"
        )
    if record.get("schema_version") != WINDOW_SCHEMA_VERSION:
        raise ValueError(f"anomaly window schema_version must be {WINDOW_SCHEMA_VERSION}")
    for field in ("run_id", "task_id"):
        value = record.get(field)
        if not isinstance(value, str) or not value:
            raise ValueError(f"anomaly window {field} must be a nonempty string")
    turn = record.get("turn")
    if isinstance(turn, bool) or not isinstance(turn, int) or turn < 0:
        raise ValueError("anomaly window turn must be a non-negative integer")
    vector(record)
    alerts = record.get("rule_alerts")
    if (
        not isinstance(alerts, list)
        or any(not isinstance(alert, str) or not alert for alert in alerts)
        or len(alerts) != len(set(alerts))
    ):
        raise ValueError("anomaly window rule_alerts must contain unique nonempty strings")
    if labeled and not isinstance(record.get("is_anomaly"), bool):
        raise ValueError("labeled anomaly window is_anomaly must be boolean")
    if "anomaly_score" in record:
        score = record["anomaly_score"]
        if score is not None and (
            isinstance(score, bool)
            or not isinstance(score, (int, float))
            or not math.isfinite(score)
            or not 0 <= score <= 1
        ):
            raise ValueError("anomaly_score must be null or a finite probability")


def window_identity(record: Mapping[str, Any]) -> tuple[str, str, int]:
    """Return the validated run/task/turn identity of one telemetry window."""
    return str(record["run_id"]), str(record["task_id"]), int(record["turn"])


def evaluate_validation(
    forest: IsolationForest,
    records: Sequence[Mapping[str, Any]],
    *,
    threshold: float,
    dataset_sha256: str,
) -> ValidationSummary:
    """Evaluate a labeled holdout containing both normal and anomaly rows."""
    validate_threshold(threshold)
    if not valid_sha256(dataset_sha256):
        raise ValueError("validation dataset_sha256 must be lowercase SHA-256 hex")
    normal_rows = anomaly_rows = true_positives = false_positives = 0
    identities: set[tuple[str, str, int]] = set()
    for index, record in enumerate(records):
        validate_window_record(record, labeled=True)
        identity = window_identity(record)
        if identity in identities:
            raise ValueError(f"validation row {index} duplicates window identity {identity}")
        identities.add(identity)
        label = record.get("is_anomaly")
        assert isinstance(label, bool)
        alert = forest.score(vector(record)) >= threshold
        if label:
            anomaly_rows += 1
            true_positives += int(alert)
        else:
            normal_rows += 1
            false_positives += int(alert)
    if normal_rows == 0 or anomaly_rows == 0:
        raise ValueError("validation input must contain both normal and anomaly rows")
    return ValidationSummary(
        dataset_sha256=dataset_sha256,
        rows=len(records),
        normal_rows=normal_rows,
        anomaly_rows=anomaly_rows,
        true_positive_rate=true_positives / anomaly_rows,
        false_positive_rate=false_positives / normal_rows,
    )


def build_artifact(
    forest: IsolationForest,
    *,
    artifact_id: str,
    dataset_sha256: str,
    training_rows: int,
    seed: int,
    threshold: float = DEFAULT_THRESHOLD,
    active_eligible: bool = False,
    validation: ValidationSummary | None = None,
    created_at: datetime | None = None,
) -> dict[str, Any]:
    """Build a strict portable artifact, defaulting to shadow-only metadata."""
    validate_threshold(threshold)
    validate_forest(forest)
    if not artifact_id.strip() or len(artifact_id) > 128:
        raise ValueError("artifact_id must contain 1 to 128 non-whitespace characters")
    if not valid_sha256(dataset_sha256):
        raise ValueError("dataset_sha256 must be lowercase SHA-256 hex")
    if training_rows < 2 or forest.sample_size > training_rows:
        raise ValueError("training_rows must cover the forest sample size")
    validate_seed(seed)
    if active_eligible and validation is None:
        raise ValueError("active eligibility requires labeled validation input")
    if validation is not None:
        validate_validation_summary(validation)
        if validation.dataset_sha256 == dataset_sha256:
            raise ValueError("training and validation inputs must have different digests")
    if active_eligible and validation is not None and not validation.passes_active_gate():
        raise ValueError("labeled validation does not pass the frozen active eligibility gates")
    timestamp = created_at or datetime.now(UTC).replace(microsecond=0)
    if timestamp.tzinfo is None or timestamp.utcoffset() is None:
        raise ValueError("created_at must be timezone-aware")
    provenance: dict[str, Any] = {
        "method": "dependency_free_isolation_forest",
        "dataset_sha256": dataset_sha256,
        "training_rows": training_rows,
        "seed": seed,
    }
    if validation is not None:
        provenance["validation"] = asdict(validation)
    return {
        "schema_version": ARTIFACT_SCHEMA_VERSION,
        "artifact_id": artifact_id,
        "feature_schema_version": FEATURE_SCHEMA_VERSION,
        "feature_names": FEATURES,
        "created_at": timestamp.astimezone(UTC).isoformat().replace("+00:00", "Z"),
        "provenance": provenance,
        "threshold": threshold,
        "active_eligible": active_eligible,
        "sample_size": forest.sample_size,
        "trees": [asdict(tree) for tree in forest.trees],
    }


def validate_forest(forest: IsolationForest) -> None:
    """Reject a forest that cannot satisfy the portable Go runtime contract."""
    if forest.sample_size < 2 or forest.sample_size > MAX_SAMPLE_SIZE:
        raise ValueError(f"forest sample_size must be between 2 and {MAX_SAMPLE_SIZE}")
    if not forest.trees or len(forest.trees) > MAX_TREES:
        raise ValueError(f"forest must contain between 1 and {MAX_TREES} trees")
    for index, tree in enumerate(forest.trees):
        if tree.size != forest.sample_size:
            raise ValueError(f"tree {index} root size must equal sample_size")
        nodes = validate_tree_node(tree, depth=0, seen=set())
        if nodes > 2 * forest.sample_size - 1:
            raise ValueError(f"tree {index} has too many nodes for sample_size")


def validate_tree_node(node: Node, *, depth: int, seen: set[int]) -> int:
    """Validate one acyclic, bounded portable tree and return its node count."""
    identity = id(node)
    if identity in seen:
        raise ValueError("isolation tree must not contain shared or cyclic nodes")
    seen.add(identity)
    if depth > MAX_TREE_DEPTH:
        raise ValueError(f"isolation tree depth exceeds {MAX_TREE_DEPTH}")
    if isinstance(node.size, bool) or not isinstance(node.size, int) or node.size < 1:
        raise ValueError("isolation tree node size must be a positive integer")
    leaf = node.feature is None and node.split is None and node.left is None and node.right is None
    branch = (
        node.feature is not None
        and node.split is not None
        and node.left is not None
        and node.right is not None
    )
    if leaf:
        return 1
    if not branch:
        raise ValueError("isolation tree node must be a complete leaf or branch")
    assert node.feature is not None
    assert node.split is not None
    assert node.left is not None
    assert node.right is not None
    if isinstance(node.feature, bool) or not 0 <= node.feature < len(FEATURES):
        raise ValueError("isolation tree feature index is outside the feature contract")
    if isinstance(node.split, bool) or not math.isfinite(node.split):
        raise ValueError("isolation tree split must be finite")
    if (
        node.size < 2
        or node.left.size >= node.size
        or node.right.size >= node.size
        or node.left.size + node.right.size != node.size
    ):
        raise ValueError("isolation tree child sizes must sum to the branch size")
    return (
        1
        + validate_tree_node(node.left, depth=depth + 1, seen=seen)
        + validate_tree_node(
            node.right,
            depth=depth + 1,
            seen=seen,
        )
    )


def validate_validation_summary(validation: ValidationSummary) -> None:
    """Validate embedded holdout provenance independently of promotion status."""
    if not valid_sha256(validation.dataset_sha256):
        raise ValueError("validation dataset_sha256 must be lowercase SHA-256 hex")
    if (
        validation.rows < 2
        or validation.normal_rows < 1
        or validation.anomaly_rows < 1
        or validation.normal_rows + validation.anomaly_rows != validation.rows
    ):
        raise ValueError("validation rows must contain both classes and match their total")
    if (
        not math.isfinite(validation.true_positive_rate)
        or not 0 <= validation.true_positive_rate <= 1
        or not math.isfinite(validation.false_positive_rate)
        or not 0 <= validation.false_positive_rate <= 1
    ):
        raise ValueError("validation rates must be finite probabilities")


def load_jsonl(path: Path, *, labeled: bool) -> tuple[list[Mapping[str, Any]], str]:
    """Load JSON objects and return the SHA-256 of the exact source bytes."""
    raw = path.read_bytes()
    records: list[Mapping[str, Any]] = []
    identities: set[tuple[str, str, int]] = set()
    for line_number, line in enumerate(raw.decode("utf-8").splitlines(), start=1):
        if not line.strip():
            continue
        value = json.loads(line)
        if not isinstance(value, Mapping):
            raise ValueError(f"{path}:{line_number}: expected a JSON object")
        try:
            validate_window_record(value, labeled=labeled)
        except ValueError as error:
            raise ValueError(f"{path}:{line_number}: {error}") from error
        identity = window_identity(value)
        if identity in identities:
            raise ValueError(f"{path}:{line_number}: duplicate window identity {identity}")
        identities.add(identity)
        records.append(value)
    if not records:
        raise ValueError(f"{path}: expected at least one JSON object")
    return records, hashlib.sha256(raw).hexdigest()


def validate_threshold(threshold: float) -> None:
    """Validate the artifact's immutable alert threshold."""
    if not math.isfinite(threshold) or threshold <= 0 or threshold > 1:
        raise ValueError("threshold must be finite and in (0,1]")


def validate_seed(seed: int) -> None:
    """Keep Python-produced artifacts portable to Go's signed int64 field."""
    if isinstance(seed, bool) or not isinstance(seed, int) or not MIN_INT64 <= seed <= MAX_INT64:
        raise ValueError("seed must be a signed 64-bit integer")


def valid_sha256(value: str) -> bool:
    """Return whether value is canonical lowercase SHA-256 hex."""
    return (
        len(value) == 64
        and value == value.lower()
        and all(character in "0123456789abcdef" for character in value)
    )


def main(argv: Sequence[str] | None = None) -> None:
    """Train and serialize a portable, shadow-only-by-default artifact."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--artifact-id")
    parser.add_argument("--trees", type=int, default=100)
    parser.add_argument("--sample-size", type=int, default=128)
    parser.add_argument("--seed", type=int, default=42)
    parser.add_argument("--threshold", type=float, default=DEFAULT_THRESHOLD)
    parser.add_argument(
        "--validation-input",
        type=Path,
        help="labeled JSONL with features and boolean is_anomaly",
    )
    parser.add_argument(
        "--active-eligible",
        action="store_true",
        help="mark eligible only when labeled validation passes frozen gates",
    )
    args = parser.parse_args(argv)
    if args.active_eligible and args.validation_input is None:
        parser.error("--active-eligible requires --validation-input")
    try:
        validate_seed(args.seed)
    except ValueError as error:
        parser.error(str(error))
    records, dataset_sha256 = load_jsonl(args.input, labeled=False)
    windows = [vector(record) for record in records]
    forest = fit(windows, trees=args.trees, sample_size=args.sample_size, seed=args.seed)
    validation: ValidationSummary | None = None
    if args.validation_input is not None:
        validation_records, validation_sha256 = load_jsonl(args.validation_input, labeled=True)
        training_identities = {window_identity(record) for record in records}
        validation_identities = {window_identity(record) for record in validation_records}
        overlap = training_identities & validation_identities
        if overlap:
            parser.error(
                "training and validation inputs contain overlapping window identities: "
                f"{sorted(overlap)[:3]}"
            )
        validation = evaluate_validation(
            forest,
            validation_records,
            threshold=args.threshold,
            dataset_sha256=validation_sha256,
        )
    try:
        artifact = build_artifact(
            forest,
            artifact_id=args.artifact_id or args.output.stem,
            dataset_sha256=dataset_sha256,
            training_rows=len(windows),
            seed=args.seed,
            threshold=args.threshold,
            active_eligible=args.active_eligible,
            validation=validation,
        )
    except ValueError as error:
        parser.error(str(error))
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("x", encoding="utf-8") as handle:
        json.dump(artifact, handle, indent=2, sort_keys=True, allow_nan=False)
        handle.write("\n")


if __name__ == "__main__":
    main()
