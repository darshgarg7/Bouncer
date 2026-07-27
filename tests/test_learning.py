from __future__ import annotations

import json
import math
import random
import unittest
from pathlib import Path

from benchmarking.learning.anomaly import FEATURES, fit
from benchmarking.learning.bandits import ConservativeLinUCB, safe_epsilon_greedy
from benchmarking.learning.evaluate import evaluate
from benchmarking.learning.features import FEATURE_NAMES, extract_features
from benchmarking.learning.markov import fit_transition_prior, sequence_log_likelihood
from benchmarking.learning.outcomes import flatten_rows, train_artifact
from benchmarking.learning.pareto import nondominated, safety_first


class FeatureTests(unittest.TestCase):
    def test_feature_extraction_is_complete_and_deterministic(self) -> None:
        context = {
            "turn": 1,
            "max_turns": 4,
            "recent_rejections": 2,
            "no_progress_streak": 1,
            "previous_operation": "filesystem.read",
            "state": {
                "completed_operations": ["filesystem.read"],
                "files": {"workspace/input": "data"},
                "mutation_count": 0,
                "constraint_feedback": [],
            },
            "policy": {"max_mutations": 2},
        }
        candidate = {
            "operation_class": "filesystem.write",
            "target": "workspace/result.txt",
            "arguments": {"content": "ready"},
            "declared_dependencies": ["filesystem.read"],
            "routing_objectives": {
                "latency_ms": 12,
                "cost_units": 0.2,
                "safety_risk": 0.1,
            },
        }
        prior = {
            "fallback_probability": 0.01,
            "probabilities": {"filesystem.read": {"filesystem.write": 0.8}},
        }
        first = extract_features(context, candidate, prior)
        second = extract_features(context, candidate, prior)
        self.assertEqual(first, second)
        self.assertEqual(set(FEATURE_NAMES), set(first))
        self.assertEqual(1.0, first["dependency_satisfaction_ratio"])
        self.assertEqual(0.0, first["transition_unseen"])

    def test_python_features_match_shared_fixture(self) -> None:
        root = Path(__file__).resolve().parents[1]
        fixture = json.loads((root / "examples/learning-feature-fixture.json").read_text())
        candidate = dict(fixture["candidate"]["candidate"])
        candidate["routing_objectives"] = fixture["candidate"]["routing_objectives"]
        actual = extract_features(
            fixture["context"],
            candidate,
            fixture["transition_prior"],
        )
        self.assertEqual(fixture["expected"], actual)


class RoutingTests(unittest.TestCase):
    def test_pareto_holding_excludes_dominated_candidate(self) -> None:
        candidates = [
            {
                "candidate_id": "fast",
                "progress": 0.8,
                "success": 0.7,
                "latency_ms": 1,
                "cost_units": 3,
                "adverse_risk": 0.1,
            },
            {
                "candidate_id": "cheap",
                "progress": 0.7,
                "success": 0.8,
                "latency_ms": 3,
                "cost_units": 1,
                "adverse_risk": 0.1,
            },
            {
                "candidate_id": "dominated",
                "progress": 0.5,
                "success": 0.5,
                "latency_ms": 4,
                "cost_units": 4,
                "adverse_risk": 0.2,
            },
        ]
        frontier = nondominated(candidates)
        self.assertEqual({"fast", "cheap"}, {item["candidate_id"] for item in frontier})
        self.assertIn(safety_first(frontier)["candidate_id"], {"fast", "cheap"})

    def test_epsilon_greedy_reports_exact_propensity(self) -> None:
        choice = safe_epsilon_greedy(
            ["a", "b", "c"],
            "a",
            epsilon=1.0,
            randomizer=random.Random(42),
        )
        self.assertNotEqual("a", choice.candidate_id)
        self.assertEqual(0.5, choice.probability)

    def test_linucb_learns_from_feature_vectors(self) -> None:
        bandit = ConservativeLinUCB(2, alpha=0)
        for _ in range(20):
            bandit.update([1, 0], 1)
            bandit.update([0, 1], 0)
        choice = bandit.choose({"good": [1, 0], "bad": [0, 1]})
        self.assertEqual("good", choice.candidate_id)


class StatisticalTests(unittest.TestCase):
    def test_markov_prior_prefers_observed_sequence(self) -> None:
        prior = fit_transition_prior(
            [
                ["filesystem.read", "filesystem.write", "task.complete"],
                ["filesystem.read", "filesystem.write", "task.complete"],
            ]
        )
        common = sequence_log_likelihood(
            ["filesystem.read", "filesystem.write", "task.complete"], prior
        )
        unusual = sequence_log_likelihood(
            ["service.deploy", "filesystem.delete", "command.run"], prior
        )
        self.assertGreater(common, unusual)

    def test_isolation_forest_scores_outlier_higher(self) -> None:
        normal = [
            [0.05 * ((row + column) % 3) for column in range(len(FEATURES))] for row in range(40)
        ]
        forest = fit(normal, trees=30, sample_size=32, seed=42)
        normal_score = forest.score(normal[0])
        outlier_score = forest.score([10.0] * len(FEATURES))
        self.assertGreater(outlier_score, normal_score)

    def test_supervised_trainer_emits_portable_artifact(self) -> None:
        trajectories = synthetic_trajectories()
        rows = flatten_rows(trajectories)
        self.assertEqual(8, len(rows))
        artifact, metrics = train_artifact(
            trajectories,
            dataset_sha256="0" * 64,
            artifact_id="test-trained",
            validation_fraction=0.25,
        )
        self.assertEqual("0.1.0", artifact["schema_version"])
        self.assertTrue(feature_union(artifact["models"]).issubset(set(FEATURE_NAMES)))
        self.assertIn("calibrated_cost_log1p", artifact["models"]["cost_units"]["coefficients"])
        self.assertGreater(metrics["rows"], 0)

    def test_known_truth_evaluation_has_positive_propensities(self) -> None:
        result = evaluate(episodes=100, seed=7, epsilon=0.05)
        self.assertTrue(result["gates"]["exact_positive_propensities"])
        self.assertGreater(result["minimum_behavior_probability"], 0)


def synthetic_trajectories() -> list[dict[str, object]]:
    trajectories: list[dict[str, object]] = []
    for run in range(4):
        transitions = []
        for step, operation in enumerate(("filesystem.read", "filesystem.write")):
            features = dict.fromkeys(FEATURE_NAMES, 0.0)
            features[f"operation={operation}"] = 1.0
            features["calibrated_latency_log1p"] = math.log1p(10 + run + step)
            features["calibrated_cost_log1p"] = math.log1p(0.1 + step)
            candidate_id = f"candidate-{step}"
            transitions.append(
                {
                    "decision": {
                        "selected_candidate_id": candidate_id,
                        "candidates": [
                            {
                                "candidate_id": candidate_id,
                                "operation_class": operation,
                                "features": features,
                            }
                        ],
                    },
                    "outcome": {
                        "progress_delta": float(step == 1),
                        "latency_ms": 10 + run + step,
                        "cost_units": None,
                        "cost_censored": True,
                        "adverse": False,
                    },
                }
            )
        trajectories.append(
            {
                "run_id": f"run-{run}",
                "passed": run % 2 == 0,
                "transitions": transitions,
            }
        )
    return trajectories


def feature_union(models: dict[str, object]) -> set[str]:
    union: set[str] = set()
    for model in models.values():
        if isinstance(model, dict):
            union.update(model["coefficients"])
    return union


if __name__ == "__main__":
    unittest.main()
