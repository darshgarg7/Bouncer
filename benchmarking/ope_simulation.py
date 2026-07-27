"""Known-ground-truth contextual-bandit validation for the OPE laboratory."""

from __future__ import annotations

import argparse
import json
import random
from pathlib import Path

from .ope import Observation, evaluate, write_exclusive


def simulate(sample_size: int, seed: int, epsilon: float = 0.2) -> list[Observation]:
    if sample_size < 1 or not 0 < epsilon < 1:
        raise ValueError("sample_size must be positive and epsilon must be in (0,1)")
    randomizer = random.Random(seed)
    observations: list[Observation] = []
    for _ in range(sample_size):
        context = randomizer.randrange(2)
        optimal_action = context
        selected = optimal_action if randomizer.random() >= epsilon else 1 - optimal_action
        behavior_probability = 1 - epsilon if selected == optimal_action else epsilon
        reward_probability = 0.8 if selected == optimal_action else 0.2
        reward = float(randomizer.random() < reward_probability)
        observations.append(
            Observation(
                reward=reward,
                behavior_probability=behavior_probability,
                target_probability=float(selected == optimal_action),
                outcome_prediction_taken=reward_probability,
                outcome_prediction_target=0.8,
                support_complete=True,
            )
        )
    return observations


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--sample-size", type=int, default=10_000)
    parser.add_argument("--seed", type=int, default=42)
    args = parser.parse_args()
    report = evaluate(
        simulate(args.sample_size, args.seed), bootstrap_samples=1_000, seed=args.seed
    )
    report["known_ground_truth"] = 0.8
    report["absolute_error"] = {
        name: abs(interval["estimate"] - 0.8) for name, interval in report["estimates"].items()
    }
    write_exclusive(args.output, report)
    print(json.dumps(report, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
