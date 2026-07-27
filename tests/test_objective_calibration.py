"""Tests for offline objective calibration fitting."""

from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path
from typing import cast

from jsonschema import Draft202012Validator

from benchmarking.objective_calibration import (
    Objectives,
    Observation,
    fit_artifact,
    load_observations,
)

ROOT = Path(__file__).resolve().parents[1]


class ObjectiveCalibrationTests(unittest.TestCase):
    """Exercise strict observation loading and held-out fitting."""

    def test_fit_artifact_uses_heldout_signal(self) -> None:
        """Useful estimates receive influence after held-out validation."""
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "observations.jsonl"
            records = []
            for index in range(100):
                hazardous = index % 2 == 1
                measured_latency = float(20 + index)
                measured_cost = float(1 + index / 20)
                records.append(
                    {
                        "operation_class": "filesystem.read",
                        "estimated_objectives": {
                            "latency_ms": measured_latency / 2,
                            "cost_units": measured_cost / 3,
                            "safety_risk": 0.8 if hazardous else 0.2,
                        },
                        "measured_objectives": {
                            "latency_ms": measured_latency,
                            "cost_units": measured_cost,
                            "safety_risk": int(hazardous),
                        },
                    }
                )
            path.write_text(
                "".join(json.dumps(record) + "\n" for record in records),
                encoding="utf-8",
            )
            observations, digest = load_observations(path)
            artifact = fit_artifact(observations, "synthetic-fit", digest)

        influence = cast(dict[str, float], artifact["model_influence"])
        self.assertGreater(influence["latency_ms"], 0)
        self.assertGreater(influence["cost_units"], 0)
        self.assertGreater(influence["safety_risk"], 0)
        provenance = cast(dict[str, object], artifact["provenance"])
        self.assertEqual(provenance["sample_count"], 100)
        self.assertEqual(provenance["validation_count"], 20)
        schema = json.loads(
            (ROOT / "schemas/objective-calibration.schema.json").read_text(encoding="utf-8")
        )
        errors = list(Draft202012Validator(schema).iter_errors(artifact))
        self.assertEqual(errors, [])

    def test_load_rejects_nonbinary_measured_risk(self) -> None:
        """The fitter requires observed outcomes, not another soft estimate."""
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "invalid.jsonl"
            path.write_text(
                json.dumps(
                    {
                        "operation_class": "filesystem.read",
                        "estimated_objectives": {
                            "latency_ms": 1,
                            "cost_units": 1,
                            "safety_risk": 0.5,
                        },
                        "measured_objectives": {
                            "latency_ms": 1,
                            "cost_units": 1,
                            "safety_risk": 0.5,
                        },
                    }
                ),
                encoding="utf-8",
            )
            with self.assertRaisesRegex(ValueError, "binary outcome"):
                load_observations(path)

    def test_load_rejects_malformed_observation_files(self) -> None:
        """Observation ingestion rejects ambiguity before fitting."""
        valid_objectives = {"latency_ms": 1, "cost_units": 1, "safety_risk": 0}
        cases: dict[str, tuple[str, str]] = {
            "empty": ("\n", "no observations"),
            "invalid json": ("{", "invalid JSON"),
            "not object": ("[]", "observation must be an object"),
            "unknown field": (
                json.dumps(
                    {
                        "operation_class": "filesystem.read",
                        "estimated_objectives": valid_objectives,
                        "measured_objectives": valid_objectives,
                        "extra": True,
                    }
                ),
                "fields must be exactly",
            ),
            "boolean number": (
                json.dumps(
                    {
                        "operation_class": "filesystem.read",
                        "estimated_objectives": {
                            "latency_ms": True,
                            "cost_units": 1,
                            "safety_risk": 0,
                        },
                        "measured_objectives": valid_objectives,
                    }
                ),
                "latency_ms must be a number",
            ),
            "risk outside range": (
                json.dumps(
                    {
                        "operation_class": "filesystem.read",
                        "estimated_objectives": {
                            "latency_ms": 1,
                            "cost_units": 1,
                            "safety_risk": 2,
                        },
                        "measured_objectives": valid_objectives,
                    }
                ),
                "safety_risk must be between",
            ),
        }
        for name, (document, message) in cases.items():
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / "observations.jsonl"
                path.write_text(document, encoding="utf-8")
                with self.assertRaisesRegex(ValueError, message):
                    load_observations(path)

    def test_fit_rejects_invalid_configuration(self) -> None:
        """Direct API callers receive the same closed configuration boundary."""
        observation = Observation(
            operation_class="filesystem.read",
            estimated=Objectives(latency_ms=1, cost_units=1, safety_risk=0.1),
            measured=Objectives(latency_ms=2, cost_units=2, safety_risk=0),
        )
        observations = [observation, observation]
        digest = "0" * 64
        cases = {
            "calibration_id": {"calibration_id": " ", "dataset_sha256": digest},
            "dataset_sha256": {"calibration_id": "fit", "dataset_sha256": "ABC"},
            "minimum_samples": {
                "calibration_id": "fit",
                "dataset_sha256": digest,
                "minimum_samples": 1,
            },
            "validation_fraction": {
                "calibration_id": "fit",
                "dataset_sha256": digest,
                "validation_fraction": 0.9,
                "minimum_samples": 2,
            },
            "latency_cap": {
                "calibration_id": "fit",
                "dataset_sha256": digest,
                "latency_cap": 0,
                "minimum_samples": 2,
            },
            "cost_cap": {
                "calibration_id": "fit",
                "dataset_sha256": digest,
                "cost_cap": float("nan"),
                "minimum_samples": 2,
            },
        }
        for name, arguments in cases.items():
            with self.subTest(name=name), self.assertRaises(ValueError):
                fit_artifact(observations, **arguments)  # type: ignore[arg-type]


if __name__ == "__main__":
    unittest.main()
