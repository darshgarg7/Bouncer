"""Fit trusted objective-calibration artifacts from measured observations.

The runtime deliberately does not train on live execution. This offline tool
fits affine latency/cost scalers, a Platt risk scaler, operation-level priors,
and conservative blend weights selected on a deterministic held-out split.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import math
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Final, cast

_OBJECTIVE_KEYS: Final = {"latency_ms", "cost_units", "safety_risk"}
_WEIGHT_GRID: Final = tuple(index / 20 for index in range(21))
_EPSILON: Final = 1e-6


@dataclass(frozen=True)
class Objectives:
    """Three objective values in their external JSON units."""

    latency_ms: float
    cost_units: float
    safety_risk: float

    def value(self, name: str) -> float:
        """Return one named objective."""
        if name == "latency_ms":
            return self.latency_ms
        if name == "cost_units":
            return self.cost_units
        if name == "safety_risk":
            return self.safety_risk
        raise ValueError(f"unknown objective {name!r}")

    def as_dict(self) -> dict[str, float]:
        """Return the JSON representation expected by the Go runtime."""
        return {
            "latency_ms": self.latency_ms,
            "cost_units": self.cost_units,
            "safety_risk": self.safety_risk,
        }


@dataclass(frozen=True)
class Observation:
    """One provider estimate paired with independently measured outcomes."""

    operation_class: str
    estimated: Objectives
    measured: Objectives

    def canonical_bytes(self) -> bytes:
        """Return a stable representation used for deterministic splitting."""
        payload = {
            "estimated_objectives": self.estimated.as_dict(),
            "measured_objectives": self.measured.as_dict(),
            "operation_class": self.operation_class,
        }
        return json.dumps(payload, sort_keys=True, separators=(",", ":")).encode()


@dataclass(frozen=True)
class Affine:
    """A non-negative affine scaler for a continuous objective."""

    scale: float
    offset: float

    def apply(self, value: float, cap: float) -> float:
        """Bound and transform one provider estimate."""
        return max(0.0, self.scale * min(max(value, 0.0), cap) + self.offset)


@dataclass(frozen=True)
class Platt:
    """A logistic scaler over the provider's reported log odds."""

    slope: float
    intercept: float

    def apply(self, probability: float) -> float:
        """Calibrate one probability using numerically stable logistic math."""
        bounded = min(max(probability, _EPSILON), 1 - _EPSILON)
        log_odds = math.log(bounded / (1 - bounded))
        value = self.slope * log_odds + self.intercept
        if value >= 0:
            return 1 / (1 + math.exp(-value))
        exponential = math.exp(value)
        return exponential / (1 + exponential)


def load_observations(path: Path) -> tuple[list[Observation], str]:
    """Read strict JSONL observations and return their source digest."""
    data = path.read_bytes()
    observations: list[Observation] = []
    for line_number, raw_line in enumerate(data.splitlines(), start=1):
        if not raw_line.strip():
            continue
        try:
            document = cast(object, json.loads(raw_line))
        except json.JSONDecodeError as error:
            raise ValueError(f"{path}:{line_number}: invalid JSON: {error.msg}") from error
        observations.append(_parse_observation(document, path, line_number))
    if not observations:
        raise ValueError(f"{path}: no observations")
    digest = hashlib.sha256(data).hexdigest()
    return observations, digest


def fit_artifact(
    observations: list[Observation],
    calibration_id: str,
    dataset_sha256: str,
    validation_fraction: float = 0.2,
    latency_cap: float = 300_000,
    cost_cap: float = 100,
    minimum_samples: int = 20,
) -> dict[str, object]:
    """Fit one runtime artifact with held-out model-influence selection."""
    if not calibration_id.strip() or len(calibration_id) > 128:
        raise ValueError("calibration_id must contain 1 to 128 non-whitespace characters")
    if len(dataset_sha256) != 64 or any(
        character not in "0123456789abcdef" for character in dataset_sha256
    ):
        raise ValueError("dataset_sha256 must contain 64 lowercase hexadecimal characters")
    if minimum_samples < 2:
        raise ValueError("minimum_samples must be at least 2")
    if len(observations) < minimum_samples:
        raise ValueError(
            f"at least {minimum_samples} observations are required, got {len(observations)}"
        )
    if not 0.1 <= validation_fraction <= 0.5:
        raise ValueError("validation_fraction must be between 0.1 and 0.5")
    if not math.isfinite(latency_cap) or latency_cap <= 0:
        raise ValueError("latency_cap must be positive and finite")
    if not math.isfinite(cost_cap) or cost_cap <= 0:
        raise ValueError("cost_cap must be positive and finite")

    training, validation = _split(observations, validation_fraction)
    priors = _fit_priors(training)
    latency = _fit_affine(training, "latency_ms", latency_cap)
    cost = _fit_affine(training, "cost_units", cost_cap)
    risk = _fit_platt(training)
    influence = {
        "latency_ms": _select_weight(
            validation, priors, "latency_ms", lambda value: latency.apply(value, latency_cap)
        ),
        "cost_units": _select_weight(
            validation, priors, "cost_units", lambda value: cost.apply(value, cost_cap)
        ),
        "safety_risk": _select_weight(validation, priors, "safety_risk", risk.apply),
    }

    return {
        "schema_version": "0.1.0",
        "calibration_id": calibration_id,
        "provenance": {
            "method": "affine_platt_with_heldout_blend_selection",
            "dataset_sha256": dataset_sha256,
            "sample_count": len(observations),
            "validation_count": len(validation),
        },
        "input_bounds": {
            "latency_ms": {"minimum": 0, "maximum": latency_cap},
            "cost_units": {"minimum": 0, "maximum": cost_cap},
            "safety_risk": {"minimum": 0, "maximum": 1},
        },
        "transforms": {
            "latency_ms": {"scale": latency.scale, "offset": latency.offset},
            "cost_units": {"scale": cost.scale, "offset": cost.offset},
            "safety_risk": {"slope": risk.slope, "intercept": risk.intercept},
        },
        "model_influence": influence,
        "operation_priors": {
            operation: objective.as_dict() for operation, objective in sorted(priors.items())
        },
    }


def _parse_observation(document: object, path: Path, line_number: int) -> Observation:
    """Validate one decoded observation without accepting implicit coercions."""
    location = f"{path}:{line_number}"
    if not isinstance(document, dict):
        raise ValueError(f"{location}: observation must be an object")
    expected = {"operation_class", "estimated_objectives", "measured_objectives"}
    if set(document) != expected:
        raise ValueError(f"{location}: fields must be exactly {sorted(expected)}")
    operation = document["operation_class"]
    if not isinstance(operation, str) or not operation.strip():
        raise ValueError(f"{location}: operation_class must be a non-empty string")
    estimated = _parse_objectives(document["estimated_objectives"], location, measured=False)
    measured = _parse_objectives(document["measured_objectives"], location, measured=True)
    return Observation(operation_class=operation, estimated=estimated, measured=measured)


def _parse_objectives(document: object, location: str, *, measured: bool) -> Objectives:
    """Validate one objective object and its probability semantics."""
    if not isinstance(document, dict) or set(document) != _OBJECTIVE_KEYS:
        raise ValueError(f"{location}: objective fields must be exactly {sorted(_OBJECTIVE_KEYS)}")
    values: dict[str, float] = {}
    for name in sorted(_OBJECTIVE_KEYS):
        raw_value = document[name]
        if isinstance(raw_value, bool) or not isinstance(raw_value, int | float):
            raise ValueError(f"{location}: {name} must be a number")
        value = float(raw_value)
        if not math.isfinite(value) or value < 0:
            raise ValueError(f"{location}: {name} must be finite and non-negative")
        values[name] = value
    if values["safety_risk"] > 1:
        raise ValueError(f"{location}: safety_risk must be between 0 and 1")
    if measured and values["safety_risk"] not in {0.0, 1.0}:
        raise ValueError(f"{location}: measured safety_risk must be a binary outcome")
    return Objectives(**values)


def _split(
    observations: list[Observation], validation_fraction: float
) -> tuple[list[Observation], list[Observation]]:
    """Create a deterministic, order-independent train/validation split."""
    ordered = sorted(
        observations,
        key=lambda item: hashlib.sha256(item.canonical_bytes()).digest(),
    )
    validation_count = max(1, round(len(ordered) * validation_fraction))
    return ordered[validation_count:], ordered[:validation_count]


def _fit_priors(observations: list[Observation]) -> dict[str, Objectives]:
    """Estimate global and operation-level means from trusted measurements."""
    grouped: dict[str, list[Observation]] = {"*": observations}
    for observation in observations:
        grouped.setdefault(observation.operation_class, []).append(observation)
    return {operation: _mean_measured(items) for operation, items in grouped.items()}


def _mean_measured(observations: list[Observation]) -> Objectives:
    """Return the arithmetic mean of measured objectives."""
    count = len(observations)
    return Objectives(
        latency_ms=math.fsum(item.measured.latency_ms for item in observations) / count,
        cost_units=math.fsum(item.measured.cost_units for item in observations) / count,
        safety_risk=math.fsum(item.measured.safety_risk for item in observations) / count,
    )


def _fit_affine(observations: list[Observation], name: str, cap: float) -> Affine:
    """Fit non-negative least-squares slope and its corresponding intercept."""
    inputs = [min(item.estimated.value(name), cap) for item in observations]
    outputs = [item.measured.value(name) for item in observations]
    mean_input = math.fsum(inputs) / len(inputs)
    mean_output = math.fsum(outputs) / len(outputs)
    variance = math.fsum((value - mean_input) ** 2 for value in inputs)
    if variance <= 1e-12:
        return Affine(scale=0.0, offset=mean_output)
    covariance = math.fsum(
        (input_value - mean_input) * (output_value - mean_output)
        for input_value, output_value in zip(inputs, outputs, strict=True)
    )
    scale = max(0.0, covariance / variance)
    return Affine(scale=scale, offset=mean_output - scale * mean_input)


def _fit_platt(observations: list[Observation]) -> Platt:
    """Fit Platt slope/intercept using regularized Newton updates."""
    inputs = [_log_odds(item.estimated.safety_risk) for item in observations]
    labels = [item.measured.safety_risk for item in observations]
    slope = 1.0
    intercept = 0.0
    regularization = 1e-6
    for _ in range(100):
        probabilities = [_logistic(slope * value + intercept) for value in inputs]
        weights = [max(probability * (1 - probability), 1e-9) for probability in probabilities]
        gradient_slope = (
            math.fsum(
                (probability - label) * value
                for probability, label, value in zip(probabilities, labels, inputs, strict=True)
            )
            + regularization * slope
        )
        gradient_intercept = math.fsum(
            probability - label for probability, label in zip(probabilities, labels, strict=True)
        )
        hessian_slope = (
            math.fsum(weight * value * value for weight, value in zip(weights, inputs, strict=True))
            + regularization
        )
        hessian_cross = math.fsum(
            weight * value for weight, value in zip(weights, inputs, strict=True)
        )
        hessian_intercept = math.fsum(weights) + regularization
        determinant = hessian_slope * hessian_intercept - hessian_cross**2
        if determinant <= 1e-15:
            break
        slope_step = (
            gradient_slope * hessian_intercept - gradient_intercept * hessian_cross
        ) / determinant
        intercept_step = (
            gradient_intercept * hessian_slope - gradient_slope * hessian_cross
        ) / determinant
        next_slope = max(0.0, slope - slope_step)
        next_intercept = intercept - intercept_step
        if abs(next_slope - slope) + abs(next_intercept - intercept) < 1e-10:
            slope, intercept = next_slope, next_intercept
            break
        slope, intercept = next_slope, next_intercept
    return Platt(slope=slope, intercept=intercept)


def _select_weight(
    observations: list[Observation],
    priors: dict[str, Objectives],
    name: str,
    transform: Callable[[float], float],
) -> float:
    """Choose the least model influence that minimizes held-out squared loss."""
    best_weight = 0.0
    best_loss = math.inf
    for weight in _WEIGHT_GRID:
        losses: list[float] = []
        for observation in observations:
            prior = priors.get(observation.operation_class, priors["*"]).value(name)
            transformed = transform(observation.estimated.value(name))
            prediction = prior * (1 - weight) + transformed * weight
            losses.append((prediction - observation.measured.value(name)) ** 2)
        loss = math.fsum(losses) / len(losses)
        if loss < best_loss - 1e-12:
            best_weight = weight
            best_loss = loss
    return best_weight


def _log_odds(probability: float) -> float:
    """Return bounded log odds for a reported risk probability."""
    bounded = min(max(probability, _EPSILON), 1 - _EPSILON)
    return math.log(bounded / (1 - bounded))


def _logistic(value: float) -> float:
    """Return a numerically stable logistic value."""
    if value >= 0:
        return 1 / (1 + math.exp(-value))
    exponential = math.exp(value)
    return exponential / (1 + exponential)


def _parser() -> argparse.ArgumentParser:
    """Build the command-line interface."""
    parser = argparse.ArgumentParser(
        description="Fit a Bouncer objective-calibration artifact from measured JSONL observations."
    )
    parser.add_argument("--input", type=Path, required=True, help="measured observation JSONL")
    parser.add_argument("--output", type=Path, required=True, help="new artifact JSON path")
    parser.add_argument("--calibration-id", required=True, help="stable artifact identifier")
    parser.add_argument("--validation-fraction", type=float, default=0.2)
    parser.add_argument("--latency-cap", type=float, default=300_000)
    parser.add_argument("--cost-cap", type=float, default=100)
    parser.add_argument("--minimum-samples", type=int, default=20)
    return parser


def main() -> None:
    """Fit and exclusively create one calibration artifact."""
    args = _parser().parse_args()
    observations, digest = load_observations(args.input)
    artifact = fit_artifact(
        observations,
        calibration_id=args.calibration_id,
        dataset_sha256=digest,
        validation_fraction=args.validation_fraction,
        latency_cap=args.latency_cap,
        cost_cap=args.cost_cap,
        minimum_samples=args.minimum_samples,
    )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("x", encoding="utf-8") as handle:
        json.dump(artifact, handle, indent=2, sort_keys=True)
        handle.write("\n")


if __name__ == "__main__":
    main()
