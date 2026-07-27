from __future__ import annotations

import io
import json
import math
import random
import unittest
from contextlib import redirect_stderr
from datetime import UTC, datetime
from pathlib import Path
from tempfile import TemporaryDirectory

from jsonschema import Draft202012Validator

from benchmarking.learning.anomaly import (
    FEATURES,
    MAX_INT64,
    IsolationForest,
    Node,
    ValidationSummary,
    build_artifact,
    evaluate_validation,
    fit,
    validate_window_record,
    vector,
)
from benchmarking.learning.anomaly import (
    main as anomaly_main,
)
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


class AnomalyArtifactTests(unittest.TestCase):
    def test_python_matches_cross_language_score_fixture(self) -> None:
        root = Path(__file__).resolve().parents[1]
        fixture = json.loads((root / "examples/anomaly-score-fixture.json").read_text())
        artifact = fixture["artifact"]

        def decode_node(value: dict[str, object]) -> Node:
            left = value["left"]
            right = value["right"]
            return Node(
                size=int(value["size"]),
                feature=None if value["feature"] is None else int(value["feature"]),
                split=None if value["split"] is None else float(value["split"]),
                left=None if left is None else decode_node(left),
                right=None if right is None else decode_node(right),
            )

        forest = IsolationForest(
            sample_size=int(artifact["sample_size"]),
            trees=[decode_node(tree) for tree in artifact["trees"]],
        )
        for case in fixture["cases"]:
            with self.subTest(case=case["name"]):
                score = forest.score(vector({"features": case["features"]}))
                self.assertAlmostEqual(case["expected_score"], score, places=15)
                self.assertEqual(case["expected_alert"], score >= artifact["threshold"])

    def test_artifact_defaults_to_shadow_only(self) -> None:
        forest = fit([[0.0] * len(FEATURES), [0.1] * len(FEATURES)], trees=2, sample_size=2)
        artifact = build_artifact(
            forest,
            artifact_id="shadow-test",
            dataset_sha256="0" * 64,
            training_rows=2,
            seed=42,
            created_at=datetime(2026, 7, 27, tzinfo=UTC),
        )
        self.assertFalse(artifact["active_eligible"])
        self.assertNotIn("validation", artifact["provenance"])
        self.assertEqual(FEATURES, artifact["feature_names"])
        self.assertEqual("2026-07-27T00:00:00Z", artifact["created_at"])

    def test_active_eligibility_requires_passing_labeled_validation(self) -> None:
        forest = separating_forest()
        records = [anomaly_window(False, index) for index in range(10)] + [
            anomaly_window(True, index + 10) for index in range(10)
        ]
        validation = evaluate_validation(
            forest,
            records,
            threshold=0.6,
            dataset_sha256="1" * 64,
        )
        self.assertTrue(validation.passes_active_gate())
        artifact = build_artifact(
            forest,
            artifact_id="active-test",
            dataset_sha256="0" * 64,
            training_rows=40,
            seed=42,
            threshold=0.6,
            active_eligible=True,
            validation=validation,
            created_at=datetime(2026, 7, 27, tzinfo=UTC),
        )
        self.assertTrue(artifact["active_eligible"])
        self.assertEqual(1.0, artifact["provenance"]["validation"]["true_positive_rate"])
        with self.assertRaisesRegex(ValueError, "requires labeled validation"):
            build_artifact(
                forest,
                artifact_id="unsafe",
                dataset_sha256="0" * 64,
                training_rows=40,
                seed=42,
                active_eligible=True,
            )
        weak = ValidationSummary("1" * 64, 20, 10, 10, 0.5, 0.0)
        with self.assertRaisesRegex(ValueError, "does not pass"):
            build_artifact(
                forest,
                artifact_id="weak",
                dataset_sha256="0" * 64,
                training_rows=40,
                seed=42,
                active_eligible=True,
                validation=weak,
            )
        reused = ValidationSummary("0" * 64, 20, 10, 10, 1.0, 0.0)
        with self.assertRaisesRegex(ValueError, "different digests"):
            build_artifact(
                forest,
                artifact_id="reused-holdout",
                dataset_sha256="0" * 64,
                training_rows=40,
                seed=42,
                active_eligible=True,
                validation=reused,
            )

    def test_seed_is_portable_to_signed_int64(self) -> None:
        artifact = build_artifact(
            separating_forest(),
            artifact_id="max-seed",
            dataset_sha256="0" * 64,
            training_rows=4,
            seed=MAX_INT64,
        )
        self.assertEqual(MAX_INT64, artifact["provenance"]["seed"])
        root = Path(__file__).resolve().parents[1]
        schema = json.loads((root / "schemas/anomaly-artifact.schema.json").read_text())
        validator = Draft202012Validator(schema)
        self.assertEqual([], list(validator.iter_errors(artifact)))
        oversized = dict(artifact)
        oversized["provenance"] = dict(artifact["provenance"])
        oversized["provenance"]["seed"] = MAX_INT64 + 1
        self.assertTrue(list(validator.iter_errors(oversized)))
        with self.assertRaisesRegex(ValueError, "signed 64-bit"):
            build_artifact(
                separating_forest(),
                artifact_id="oversized-seed",
                dataset_sha256="0" * 64,
                training_rows=4,
                seed=MAX_INT64 + 1,
            )

    def test_artifact_builder_rejects_malformed_forest_and_provenance(self) -> None:
        malformed = IsolationForest(
            sample_size=2,
            trees=[Node(size=2, feature=0, split=0.5, left=Node(size=1))],
        )
        with self.assertRaisesRegex(ValueError, "complete leaf or branch"):
            build_artifact(
                malformed,
                artifact_id="malformed",
                dataset_sha256="0" * 64,
                training_rows=2,
                seed=42,
            )
        invalid_validation = ValidationSummary("BAD", 20, 10, 10, 1.0, 0.0)
        with self.assertRaisesRegex(ValueError, "validation dataset_sha256"):
            build_artifact(
                separating_forest(),
                artifact_id="bad-provenance",
                dataset_sha256="0" * 64,
                training_rows=40,
                seed=42,
                validation=invalid_validation,
            )

    def test_cli_emits_strict_artifact_with_source_digest(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "windows.jsonl"
            source.write_text(
                "\n".join(json.dumps(training_anomaly_window(index)) for index in range(4)) + "\n",
                encoding="utf-8",
            )
            output = root / "forest.json"
            anomaly_main(
                [
                    "--input",
                    str(source),
                    "--output",
                    str(output),
                    "--trees",
                    "2",
                    "--sample-size",
                    "4",
                ]
            )
            artifact = json.loads(output.read_text(encoding="utf-8"))
            self.assertFalse(artifact["active_eligible"])
            self.assertEqual("forest", artifact["artifact_id"])
            self.assertEqual(4, artifact["provenance"]["training_rows"])

    def test_cli_rejects_overlapping_train_validation_identities(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            training = root / "training.jsonl"
            training.write_text(
                "\n".join(json.dumps(training_anomaly_window(index)) for index in range(2)) + "\n",
                encoding="utf-8",
            )
            validation = root / "validation.jsonl"
            validation.write_text(
                "\n".join(
                    json.dumps(record)
                    for record in (anomaly_window(True, 0), anomaly_window(False, 2))
                )
                + "\n",
                encoding="utf-8",
            )
            with redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                anomaly_main(
                    [
                        "--input",
                        str(training),
                        "--validation-input",
                        str(validation),
                        "--output",
                        str(root / "artifact.json"),
                        "--trees",
                        "1",
                        "--sample-size",
                        "2",
                    ]
                )

    def test_validation_rejects_unlabeled_or_single_class_input(self) -> None:
        forest = separating_forest()
        with self.assertRaisesRegex(ValueError, "is_anomaly"):
            evaluate_validation(
                forest,
                [training_anomaly_window(0)],
                threshold=0.6,
                dataset_sha256="1" * 64,
            )
        with self.assertRaisesRegex(ValueError, "both normal and anomaly"):
            evaluate_validation(
                forest,
                [anomaly_window(False, 0), anomaly_window(False, 1)],
                threshold=0.6,
                dataset_sha256="1" * 64,
            )
        with self.assertRaisesRegex(ValueError, "duplicates window identity"):
            evaluate_validation(
                forest,
                [anomaly_window(False, 0), anomaly_window(True, 0)],
                threshold=0.6,
                dataset_sha256="1" * 64,
            )

    def test_training_and_validation_windows_match_published_schemas(self) -> None:
        root = Path(__file__).resolve().parents[1]
        cases = (
            ("anomaly-window.schema.json", training_anomaly_window(0), False),
            ("anomaly-validation-window.schema.json", anomaly_window(True, 1), True),
        )
        for schema_name, record, labeled in cases:
            with self.subTest(schema=schema_name):
                schema = json.loads((root / "schemas" / schema_name).read_text())
                validator = Draft202012Validator(schema)
                self.assertEqual([], list(validator.iter_errors(record)))
                validate_window_record(record, labeled=labeled)

        malformed = anomaly_window(True, 2)
        malformed["unexpected"] = True
        self.assertTrue(list(validator.iter_errors(malformed)))
        with self.assertRaisesRegex(ValueError, "unexpected"):
            validate_window_record(malformed, labeled=True)


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


def separating_forest() -> IsolationForest:
    return IsolationForest(
        sample_size=4,
        trees=[
            Node(
                size=4,
                feature=0,
                split=0.5,
                left=Node(
                    size=3,
                    feature=2,
                    split=2.5,
                    left=Node(size=2),
                    right=Node(size=1),
                ),
                right=Node(size=1),
            )
        ],
    )


def anomaly_window(is_anomaly: bool, index: int = 0) -> dict[str, object]:
    return {
        "schema_version": "0.1.0",
        "run_id": f"anomaly-run-{index}",
        "task_id": "anomaly-task",
        "turn": index,
        "features": {
            "rejection_rate": float(is_anomaly),
            "retry_rate": 0.0,
            "no_progress_streak": 0,
            "tool_switch_rate": 0.0,
            "latency_delta_ms": 0.0,
            "transition_nll": 0.0,
        },
        "rule_alerts": [],
        "is_anomaly": is_anomaly,
    }


def training_anomaly_window(index: int) -> dict[str, object]:
    record = anomaly_window(False, index)
    del record["is_anomaly"]
    return record


if __name__ == "__main__":
    unittest.main()
