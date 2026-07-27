from __future__ import annotations

import math
import unittest

from benchmarking.ope import Observation, evaluate, parse_observation
from benchmarking.ope_simulation import simulate


class OfflinePolicyEvaluationTests(unittest.TestCase):
    def test_known_ground_truth_estimators_are_close(self) -> None:
        report = evaluate(simulate(20_000, 7), bootstrap_samples=200, seed=7)
        self.assertTrue(report["admissible"], report["admission_failures"])
        for interval in report["estimates"].values():
            self.assertLess(abs(interval["estimate"] - 0.8), 0.03)
            self.assertLessEqual(interval["lower"], 0.8)
            self.assertGreaterEqual(interval["upper"], 0.8)

    def test_support_and_weight_gates_fail_closed(self) -> None:
        observations = [
            Observation(
                reward=1,
                behavior_probability=0.001,
                target_probability=1,
                outcome_prediction_taken=0.5,
                outcome_prediction_target=0.5,
                support_complete=False,
            )
            for _ in range(100)
        ]
        report = evaluate(observations, bootstrap_samples=100)
        self.assertFalse(report["admissible"])
        self.assertGreaterEqual(len(report["admission_failures"]), 2)

    def test_parser_rejects_non_finite_values(self) -> None:
        with self.assertRaisesRegex(ValueError, "finite"):
            parse_observation(
                {
                    "reward": math.nan,
                    "behavior_probability": 1,
                    "target_probability": 1,
                    "outcome_prediction_taken": 0,
                    "outcome_prediction_target": 0,
                    "support_complete": True,
                },
                1,
            )


if __name__ == "__main__":
    unittest.main()
