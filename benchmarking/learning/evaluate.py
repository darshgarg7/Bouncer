"""Evaluate Pareto holding and safe exploration against known simulator truth."""

from __future__ import annotations

import argparse
import json
import random
import statistics
from pathlib import Path
from typing import Any

from .bandits import safe_epsilon_greedy
from .pareto import nondominated, safety_first
from .simulator import Arm, Simulator


def evaluate(*, episodes: int = 1_000, seed: int = 42, epsilon: float = 0.05) -> dict[str, Any]:
    """Run a reproducible known-truth policy comparison."""
    if episodes < 100:
        raise ValueError("evaluation requires at least 100 episodes")
    simulator = Simulator(seed=seed, drift_after=episodes * 3 // 2)
    randomizer = random.Random(seed)
    outcomes: dict[str, list[dict[str, float | bool]]] = {
        "lexicographic": [],
        "pareto_safety_first": [],
        "epsilon_pareto": [],
    }
    propensities: list[float] = []
    for _ in range(episodes):
        safe = [arm for arm in simulator.candidates() if arm.safe and arm.adverse_risk <= 0.25]
        lexicographic = min(
            safe,
            key=lambda arm: (arm.adverse_risk, arm.cost_units, arm.latency_ms, arm.candidate_id),
        )
        records = [{"candidate_id": arm.candidate_id, **arm.objectives()} for arm in safe]
        frontier_records = nondominated(records)
        pareto_record = safety_first(frontier_records)
        pareto = find_arm(safe, str(pareto_record["candidate_id"]))
        choice = safe_epsilon_greedy(
            [str(record["candidate_id"]) for record in frontier_records],
            pareto.candidate_id,
            epsilon=epsilon,
            randomizer=randomizer,
        )
        explored = find_arm(safe, choice.candidate_id)
        outcomes["lexicographic"].append(simulator.execute(lexicographic))
        outcomes["pareto_safety_first"].append(simulator.execute(pareto))
        outcomes["epsilon_pareto"].append(simulator.execute(explored))
        propensities.append(choice.probability)
    summaries = {name: summarize(values) for name, values in outcomes.items()}
    return {
        "schema_version": "0.1.0",
        "episodes": episodes,
        "seed": seed,
        "epsilon": epsilon,
        "policies": summaries,
        "minimum_behavior_probability": min(propensities),
        "gates": {
            "pareto_safe": summaries["pareto_safety_first"]["adverse_rate"]
            <= summaries["lexicographic"]["adverse_rate"] + 0.01,
            "exact_positive_propensities": min(propensities) > 0,
        },
    }


def find_arm(arms: list[Arm], candidate_id: str) -> Arm:
    """Resolve one simulator arm by stable ID."""
    return next(arm for arm in arms if arm.candidate_id == candidate_id)


def summarize(outcomes: list[dict[str, float | bool]]) -> dict[str, float]:
    """Aggregate policy outcomes."""
    return {
        "progress_rate": statistics.fmean(float(outcome["progress"]) for outcome in outcomes),
        "success_rate": statistics.fmean(float(outcome["success"]) for outcome in outcomes),
        "mean_latency_ms": statistics.fmean(float(outcome["latency_ms"]) for outcome in outcomes),
        "mean_cost_units": statistics.fmean(float(outcome["cost_units"]) for outcome in outcomes),
        "adverse_rate": statistics.fmean(float(outcome["adverse"]) for outcome in outcomes),
    }


def main() -> None:
    """Run the known-truth learning evaluation."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--episodes", type=int, default=1_000)
    parser.add_argument("--seed", type=int, default=42)
    parser.add_argument("--epsilon", type=float, default=0.05)
    args = parser.parse_args()
    result = evaluate(episodes=args.episodes, seed=args.seed, epsilon=args.epsilon)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("x", encoding="utf-8") as handle:
        json.dump(result, handle, indent=2, sort_keys=True, allow_nan=False)
        handle.write("\n")


if __name__ == "__main__":
    main()
