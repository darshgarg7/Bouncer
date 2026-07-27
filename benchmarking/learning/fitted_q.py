"""Linear fitted-Q evaluation for fixed logged policies and vector rewards."""

from __future__ import annotations

import argparse
import math
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .features import FEATURE_NAMES
from .outcomes import read_trajectories, solve_ridge, write_exclusive

OBJECTIVES = ("progress", "success", "latency_ms", "cost_units", "adverse_risk")


@dataclass(frozen=True)
class Transition:
    """One fixed-policy feature transition and vector reward."""

    features: list[float]
    next_features: list[float] | None
    rewards: dict[str, float | None]


def prepare(trajectories: Sequence[Mapping[str, Any]]) -> list[Transition]:
    """Convert joined trajectories into fixed-policy FQE rows."""
    rows: list[Transition] = []
    for trajectory in trajectories:
        passed = float(bool(trajectory.get("passed")))
        raw_transitions = trajectory.get("transitions")
        if not isinstance(raw_transitions, list):
            raise ValueError("trajectory transitions must be an array")
        vectors = [selected_features(transition) for transition in raw_transitions]
        for index, raw in enumerate(raw_transitions):
            if not isinstance(raw, Mapping):
                raise ValueError("transition must be an object")
            outcome = raw.get("outcome")
            if not isinstance(outcome, Mapping):
                raise ValueError("transition outcome must be an object")
            cost = None if bool(outcome.get("cost_censored")) else number(outcome, "cost_units")
            rows.append(
                Transition(
                    features=vectors[index],
                    next_features=vectors[index + 1] if index + 1 < len(vectors) else None,
                    rewards={
                        "progress": number(outcome, "progress_delta"),
                        "success": passed if index + 1 == len(vectors) else 0.0,
                        "latency_ms": -number(outcome, "latency_ms"),
                        "cost_units": -cost if cost is not None else None,
                        "adverse_risk": -float(bool(outcome.get("adverse"))),
                    },
                )
            )
    if not rows:
        raise ValueError("FQE requires at least one transition")
    return rows


def fit(
    transitions: Sequence[Transition],
    *,
    gamma: float = 1.0,
    ridge: float = 1.0,
    iterations: int = 25,
) -> dict[str, Any]:
    """Fit independent linear Q functions for every observed objective."""
    if not 0 <= gamma <= 1 or iterations < 1 or ridge <= 0:
        raise ValueError("invalid FQE gamma, ridge, or iteration count")
    models: dict[str, Any] = {}
    for objective in OBJECTIVES:
        usable = [row for row in transitions if row.rewards[objective] is not None]
        if not usable:
            models[objective] = None
            continue
        coefficients = [0.0] * (len(FEATURE_NAMES) + 1)
        design = [[1.0, *row.features] for row in usable]
        for _ in range(iterations):
            targets = [
                observed_reward(row, objective)
                + (
                    gamma * linear_predict(coefficients, row.next_features)
                    if row.next_features is not None
                    else 0.0
                )
                for row in usable
            ]
            coefficients = solve_ridge(design, targets, ridge)
        models[objective] = {
            "intercept": coefficients[0],
            "coefficients": {
                name: coefficient
                for name, coefficient in zip(FEATURE_NAMES, coefficients[1:], strict=True)
                if abs(coefficient) >= 1e-12
            },
        }
    return {
        "schema_version": "0.1.0",
        "method": "fixed_logged_policy_linear_fqe",
        "gamma": gamma,
        "ridge": ridge,
        "iterations": iterations,
        "objectives": models,
    }


def observed_reward(transition: Transition, objective: str) -> float:
    """Return an objective after the caller has removed censored rows."""
    reward = transition.rewards[objective]
    if reward is None:
        raise ValueError(f"objective {objective} is unexpectedly censored")
    return reward


def selected_features(transition: object) -> list[float]:
    """Return the selected action's frozen feature vector."""
    if not isinstance(transition, Mapping):
        raise ValueError("transition must be an object")
    decision = transition.get("decision")
    if not isinstance(decision, Mapping):
        raise ValueError("decision must be an object")
    selected = decision.get("selected_candidate_id")
    candidates = decision.get("candidates")
    if not isinstance(candidates, list):
        raise ValueError("decision candidates must be an array")
    for candidate in candidates:
        if not isinstance(candidate, Mapping) or candidate.get("candidate_id") != selected:
            continue
        features = candidate.get("features")
        if not isinstance(features, Mapping) or set(features) != set(FEATURE_NAMES):
            raise ValueError("selected candidate feature schema is incomplete")
        return [number(features, name) for name in FEATURE_NAMES]
    raise ValueError("selected candidate is absent from the safe set")


def linear_predict(coefficients: Sequence[float], features: Sequence[float] | None) -> float:
    """Evaluate a dense linear Q model."""
    if features is None:
        return 0.0
    return coefficients[0] + sum(
        coefficient * feature
        for coefficient, feature in zip(coefficients[1:], features, strict=True)
    )


def number(value: Mapping[str, Any], key: str) -> float:
    """Read one finite numeric field."""
    result = value.get(key)
    if isinstance(result, bool) or not isinstance(result, int | float):
        raise ValueError(f"{key} must be numeric")
    number_value = float(result)
    if not math.isfinite(number_value):
        raise ValueError(f"{key} must be finite")
    return number_value


def main() -> None:
    """Fit a fixed-policy vector FQE artifact."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--gamma", type=float, default=1.0)
    parser.add_argument("--ridge", type=float, default=1.0)
    parser.add_argument("--iterations", type=int, default=25)
    args = parser.parse_args()
    trajectories, _ = read_trajectories(args.input)
    write_exclusive(
        args.output,
        fit(
            prepare(trajectories),
            gamma=args.gamma,
            ridge=args.ridge,
            iterations=args.iterations,
        ),
    )


if __name__ == "__main__":
    main()
