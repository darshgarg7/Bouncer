"""Train portable supervised outcome models from completed trajectories."""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import statistics
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from .features import FEATURE_NAMES, FEATURE_SCHEMA_VERSION
from .markov import fit_transition_prior


@dataclass(frozen=True)
class TrainingRow:
    """One selected action joined to trusted immediate and delayed labels."""

    run_id: str
    operation: str
    features: dict[str, float]
    progress: float
    success: float
    latency_ms: float
    cost_units: float | None
    adverse_risk: float


def read_trajectories(path: Path) -> tuple[list[dict[str, Any]], str]:
    """Read trajectory JSONL and return its content digest."""
    data = path.read_bytes()
    trajectories: list[dict[str, Any]] = []
    for line_number, line in enumerate(data.decode().splitlines(), start=1):
        if not line.strip():
            continue
        value = json.loads(line)
        if not isinstance(value, dict):
            raise ValueError(f"line {line_number}: trajectory must be an object")
        trajectories.append(value)
    if not trajectories:
        raise ValueError("training requires at least one trajectory")
    return trajectories, hashlib.sha256(data).hexdigest()


def flatten_rows(trajectories: Sequence[Mapping[str, Any]]) -> list[TrainingRow]:
    """Extract selected-action features and labels from joined trajectories."""
    rows: list[TrainingRow] = []
    for trajectory in trajectories:
        run_id = _string(trajectory, "run_id")
        passed = float(_boolean(trajectory, "passed"))
        transitions = trajectory.get("transitions")
        if not isinstance(transitions, list) or not transitions:
            raise ValueError(f"{run_id}: transitions must be a non-empty array")
        for transition in transitions:
            if not isinstance(transition, Mapping):
                raise ValueError(f"{run_id}: transition must be an object")
            decision = _mapping(transition, "decision")
            outcome = _mapping(transition, "outcome")
            selected_id = _string(decision, "selected_candidate_id")
            candidates = decision.get("candidates")
            if not isinstance(candidates, list):
                raise ValueError(f"{run_id}: decision candidates must be an array")
            selected = next(
                (
                    candidate
                    for candidate in candidates
                    if isinstance(candidate, Mapping)
                    and candidate.get("candidate_id") == selected_id
                ),
                None,
            )
            if not isinstance(selected, Mapping):
                raise ValueError(f"{run_id}: selected candidate is absent from safe set")
            raw_features = selected.get("features")
            if not isinstance(raw_features, Mapping):
                raise ValueError(
                    f"{run_id}: selected candidate has no features; collect logs in shadow mode"
                )
            features = _features(raw_features)
            cost_censored = _boolean(outcome, "cost_censored")
            cost_value = None
            if not cost_censored:
                cost_value = _number(outcome, "cost_units", minimum=0)
            rows.append(
                TrainingRow(
                    run_id=run_id,
                    operation=_string(selected, "operation_class"),
                    features=features,
                    progress=float(_number(outcome, "progress_delta") > 0),
                    success=passed,
                    latency_ms=_number(outcome, "latency_ms", minimum=0),
                    cost_units=cost_value,
                    adverse_risk=float(_boolean(outcome, "adverse")),
                )
            )
    if not rows:
        raise ValueError("training requires at least one selected action")
    return rows


def train_artifact(
    trajectories: Sequence[Mapping[str, Any]],
    *,
    dataset_sha256: str,
    artifact_id: str,
    validation_fraction: float = 0.2,
    ridge: float = 1.0,
    confidence_multiplier: float = 1.96,
) -> tuple[dict[str, Any], dict[str, Any]]:
    """Fit inspectable GLMs and return an artifact plus validation metrics."""
    if not 0 <= validation_fraction < 0.5:
        raise ValueError("validation_fraction must be in [0,0.5)")
    if not math.isfinite(ridge) or ridge <= 0:
        raise ValueError("ridge must be positive and finite")
    rows = flatten_rows(trajectories)
    training, validation = split_by_run(rows, validation_fraction)
    models = {
        "progress": fit_logistic(training, lambda row: row.progress, ridge=ridge),
        "success": fit_logistic(training, lambda row: row.success, ridge=ridge),
        "latency_ms": fit_log_regression(
            training,
            lambda row: row.latency_ms,
            ridge=ridge,
        ),
        "cost_units": fit_optional_cost(training, ridge=ridge),
        "adverse_risk": fit_logistic(
            training,
            lambda row: row.adverse_risk,
            ridge=ridge,
        ),
    }
    operations = [
        [
            _string(
                next(
                    candidate
                    for candidate in _mapping(transition, "decision")["candidates"]
                    if candidate["candidate_id"]
                    == _mapping(transition, "decision")["selected_candidate_id"]
                ),
                "operation_class",
            )
            for transition in trajectory["transitions"]
        ]
        for trajectory in trajectories
    ]
    artifact = {
        "schema_version": "0.1.0",
        "artifact_id": artifact_id,
        "feature_schema_version": FEATURE_SCHEMA_VERSION,
        "created_at": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
        "provenance": {
            "method": "trajectory_split_glm_ridge_v1",
            "dataset_sha256": dataset_sha256,
            "training_rows": len(training),
            "validation_rows": len(validation),
        },
        "confidence_multiplier": confidence_multiplier,
        "models": models,
        "transition_prior": fit_transition_prior(operations),
    }
    metrics = evaluate_models(models, validation or training)
    return artifact, metrics


def split_by_run(
    rows: Sequence[TrainingRow], validation_fraction: float
) -> tuple[list[TrainingRow], list[TrainingRow]]:
    """Use a stable trajectory split so events from one run never leak."""
    run_ids = sorted({row.run_id for row in rows})
    validation_runs = (
        max(1, round(len(run_ids) * validation_fraction))
        if validation_fraction > 0 and len(run_ids) > 1
        else 0
    )
    validation_set = set(run_ids[-validation_runs:]) if validation_runs else set()
    training = [row for row in rows if row.run_id not in validation_set]
    validation = [row for row in rows if row.run_id in validation_set]
    if not training:
        raise ValueError("trajectory split left no training rows")
    return training, validation


def fit_logistic(
    rows: Sequence[TrainingRow],
    label: Callable[[TrainingRow], float],
    *,
    ridge: float,
    iterations: int = 800,
) -> dict[str, Any]:
    """Fit L2-regularized logistic regression with standardized gradients."""
    matrix, means, scales = standardized_matrix(rows)
    labels = [label(row) for row in rows]
    average = min(max(statistics.fmean(labels), 1e-6), 1 - 1e-6)
    intercept = math.log(average / (1 - average))
    weights = [0.0] * len(FEATURE_NAMES)
    sample_count = len(rows)
    for iteration in range(iterations):
        gradient_intercept = 0.0
        gradients = [0.0] * len(weights)
        for values, expected in zip(matrix, labels, strict=True):
            score = intercept + sum(
                weight * value for weight, value in zip(weights, values, strict=True)
            )
            prediction = sigmoid(score)
            error = prediction - expected
            gradient_intercept += error
            for index, value in enumerate(values):
                gradients[index] += error * value
        step = 0.2 / math.sqrt(1 + iteration / 50)
        intercept -= step * gradient_intercept / sample_count
        for index in range(len(weights)):
            regularized = gradients[index] / sample_count + ridge * weights[index] / sample_count
            weights[index] -= step * regularized
    original_intercept, coefficients = unstandardize(intercept, weights, means, scales)
    predictions = [sigmoid(original_intercept + dot(coefficients, row.features)) for row in rows]
    uncertainty = math.sqrt(
        statistics.fmean(
            (prediction - expected) ** 2
            for prediction, expected in zip(predictions, labels, strict=True)
        )
    )
    return {
        "link": "logit",
        "intercept": original_intercept,
        "coefficients": sparse_coefficients(coefficients),
        "uncertainty": uncertainty,
    }


def fit_log_regression(
    rows: Sequence[TrainingRow],
    label: Callable[[TrainingRow], float],
    *,
    ridge: float,
) -> dict[str, Any]:
    """Fit ridge regression in log1p output space."""
    matrix, means, scales = standardized_matrix(rows)
    labels = [math.log1p(label(row)) for row in rows]
    design = [[1.0, *values] for values in matrix]
    coefficients = solve_ridge(design, labels, ridge)
    original_intercept, original_weights = unstandardize(
        coefficients[0], coefficients[1:], means, scales
    )
    predictions = [
        max(math.expm1(original_intercept + dot(original_weights, row.features)), 0.0)
        for row in rows
    ]
    actual = [label(row) for row in rows]
    uncertainty = math.sqrt(
        statistics.fmean(
            (prediction - expected) ** 2
            for prediction, expected in zip(predictions, actual, strict=True)
        )
    )
    return {
        "link": "log1p",
        "intercept": original_intercept,
        "coefficients": sparse_coefficients(original_weights),
        "uncertainty": uncertainty,
    }


def fit_optional_cost(rows: Sequence[TrainingRow], *, ridge: float) -> dict[str, Any]:
    """Fit measured cost or retain calibrated cost as a marked proxy."""
    observed = [row for row in rows if row.cost_units is not None]
    if not observed:
        return {
            "link": "log1p",
            "intercept": 0.0,
            "coefficients": {"calibrated_cost_log1p": 1.0},
            "uncertainty": 1.0,
        }
    return fit_log_regression(
        observed,
        observed_cost,
        ridge=ridge,
    )


def observed_cost(row: TrainingRow) -> float:
    """Return a cost after the caller has removed censored rows."""
    if row.cost_units is None:
        raise ValueError("observed cost row is unexpectedly censored")
    return row.cost_units


def standardized_matrix(
    rows: Sequence[TrainingRow],
) -> tuple[list[list[float]], list[float], list[float]]:
    """Standardize the frozen feature order for numerically stable fitting."""
    raw = [[row.features[name] for name in FEATURE_NAMES] for row in rows]
    means = [statistics.fmean(column) for column in zip(*raw, strict=True)]
    scales = []
    for index, mean in enumerate(means):
        variance = statistics.fmean((row[index] - mean) ** 2 for row in raw)
        scales.append(math.sqrt(variance) if variance > 1e-12 else 1.0)
    matrix = [
        [(value - means[index]) / scales[index] for index, value in enumerate(row)] for row in raw
    ]
    return matrix, means, scales


def solve_ridge(
    matrix: Sequence[Sequence[float]], labels: Sequence[float], ridge: float
) -> list[float]:
    """Solve normal equations with pivoted Gaussian elimination."""
    width = len(matrix[0])
    normal = [[0.0] * width for _ in range(width)]
    target = [0.0] * width
    for row, label in zip(matrix, labels, strict=True):
        for left in range(width):
            target[left] += row[left] * label
            for right in range(width):
                normal[left][right] += row[left] * row[right]
    for index in range(1, width):
        normal[index][index] += ridge
    augmented = [normal[index] + [target[index]] for index in range(width)]
    for column in range(width):
        pivot = max(range(column, width), key=lambda row: abs(augmented[row][column]))
        augmented[column], augmented[pivot] = augmented[pivot], augmented[column]
        divisor = augmented[column][column]
        if abs(divisor) < 1e-12:
            divisor = 1e-12
        augmented[column] = [value / divisor for value in augmented[column]]
        for row_index in range(width):
            if row_index == column:
                continue
            factor = augmented[row_index][column]
            augmented[row_index] = [
                value - factor * pivot_value
                for value, pivot_value in zip(augmented[row_index], augmented[column], strict=True)
            ]
    return [augmented[index][-1] for index in range(width)]


def unstandardize(
    intercept: float,
    weights: Sequence[float],
    means: Sequence[float],
    scales: Sequence[float],
) -> tuple[float, dict[str, float]]:
    """Convert standardized coefficients to raw runtime features."""
    coefficients = {
        name: weight / scale
        for name, weight, scale in zip(FEATURE_NAMES, weights, scales, strict=True)
    }
    original_intercept = intercept - sum(
        coefficients[name] * mean for name, mean in zip(FEATURE_NAMES, means, strict=True)
    )
    return original_intercept, coefficients


def evaluate_models(models: Mapping[str, Any], rows: Sequence[TrainingRow]) -> dict[str, Any]:
    """Compute transparent held-out loss metrics for promotion reports."""
    labels: dict[str, list[float]] = {
        "progress": [row.progress for row in rows],
        "success": [row.success for row in rows],
        "latency_ms": [row.latency_ms for row in rows],
        "adverse_risk": [row.adverse_risk for row in rows],
    }
    metrics: dict[str, Any] = {"rows": len(rows)}
    for name, expected in labels.items():
        predictions = [predict(models[name], row.features) for row in rows]
        metrics[name] = {
            "rmse": math.sqrt(
                statistics.fmean(
                    (prediction - actual) ** 2
                    for prediction, actual in zip(predictions, expected, strict=True)
                )
            ),
            "mean_prediction": statistics.fmean(predictions),
            "mean_label": statistics.fmean(expected),
        }
    return metrics


def predict(model: Mapping[str, Any], features: Mapping[str, float]) -> float:
    """Evaluate the same portable model links used by Go."""
    coefficients = _mapping(model, "coefficients")
    score = _number(model, "intercept") + sum(
        _finite(value, f"coefficient {name}") * features[name]
        for name, value in coefficients.items()
    )
    link = model.get("link")
    if link == "logit":
        return sigmoid(score)
    if link == "log1p":
        return max(math.expm1(min(score, 700)), 0.0)
    return score


def sigmoid(value: float) -> float:
    """Compute a numerically stable logistic link."""
    if value >= 0:
        return 1 / (1 + math.exp(-value))
    exponential = math.exp(value)
    return exponential / (1 + exponential)


def dot(coefficients: Mapping[str, float], features: Mapping[str, float]) -> float:
    """Compute a named sparse dot product."""
    return sum(coefficient * features[name] for name, coefficient in coefficients.items())


def sparse_coefficients(coefficients: Mapping[str, float]) -> dict[str, float]:
    """Remove numerical noise while retaining an auditable named model."""
    return {
        name: coefficient for name, coefficient in coefficients.items() if abs(coefficient) >= 1e-12
    }


def write_exclusive(path: Path, value: object) -> None:
    """Write an immutable JSON artifact."""
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("x", encoding="utf-8") as handle:
        json.dump(value, handle, indent=2, sort_keys=True, allow_nan=False)
        handle.write("\n")


def _features(value: Mapping[str, Any]) -> dict[str, float]:
    if set(value) != set(FEATURE_NAMES):
        missing = sorted(set(FEATURE_NAMES) - set(value))
        extra = sorted(set(value) - set(FEATURE_NAMES))
        raise ValueError(f"feature schema mismatch; missing={missing}, extra={extra}")
    return {name: _finite(value[name], f"feature {name}") for name in FEATURE_NAMES}


def _mapping(value: Mapping[str, Any], key: str) -> Mapping[str, Any]:
    result = value.get(key)
    if not isinstance(result, Mapping):
        raise ValueError(f"{key} must be an object")
    return result


def _string(value: Mapping[str, Any], key: str) -> str:
    result = value.get(key)
    if not isinstance(result, str) or not result:
        raise ValueError(f"{key} must be a non-empty string")
    return result


def _boolean(value: Mapping[str, Any], key: str) -> bool:
    result = value.get(key)
    if not isinstance(result, bool):
        raise ValueError(f"{key} must be boolean")
    return result


def _number(value: Mapping[str, Any], key: str, *, minimum: float | None = None) -> float:
    number = _finite(value.get(key), key)
    if minimum is not None and number < minimum:
        raise ValueError(f"{key} must be at least {minimum}")
    return number


def _finite(value: object, label: str) -> float:
    if isinstance(value, bool) or not isinstance(value, int | float):
        raise ValueError(f"{label} must be numeric")
    number = float(value)
    if not math.isfinite(number):
        raise ValueError(f"{label} must be finite")
    return number


def main() -> None:
    """Fit an immutable Go-compatible artifact and validation report."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True, help="trajectory JSONL")
    parser.add_argument("--artifact", type=Path, required=True, help="new artifact JSON")
    parser.add_argument("--report", type=Path, required=True, help="new validation report")
    parser.add_argument("--artifact-id", required=True)
    parser.add_argument("--validation-fraction", type=float, default=0.2)
    parser.add_argument("--ridge", type=float, default=1.0)
    args = parser.parse_args()
    trajectories, digest = read_trajectories(args.input)
    artifact, report = train_artifact(
        trajectories,
        dataset_sha256=digest,
        artifact_id=args.artifact_id,
        validation_fraction=args.validation_fraction,
        ridge=args.ridge,
    )
    write_exclusive(args.artifact, artifact)
    write_exclusive(args.report, report)


if __name__ == "__main__":
    main()
