from __future__ import annotations

import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from benchmarking.evaluate import compare_conditions, render_report, run_bouncer, summarize
from benchmarking.provider_evaluate import RunStore


class ProviderEvaluationTests(unittest.TestCase):
    def test_bouncer_subprocess_receives_explicit_provider_key(self) -> None:
        with patch("benchmarking.evaluate.subprocess.run") as run:
            run.return_value.returncode = 0
            run.return_value.stdout = "{}"
            run.return_value.stderr = ""
            run_bouncer(
                Path("bin/bouncer-run"),
                Path("benchmarks/tasks/task-001.json"),
                0,
                "https://provider.example/v1",
                api_key="test-key",
            )
        self.assertEqual(run.call_args.kwargs["env"]["NIM_API_KEY"], "test-key")

    def test_run_store_resumes_only_matching_configuration(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "run"
            store = RunStore(root)
            metadata = {"configuration_fingerprint": "abc"}
            store.initialize(metadata, resume=False)
            record = {
                "condition": "langgraph",
                "task_id": "task-001",
                "seed": 0,
            }
            store.save_record("langgraph", "task-001", 0, record)
            self.assertEqual(store.load_record("langgraph", "task-001", 0), record)

            resumed = RunStore(root)
            resumed.initialize(metadata, resume=True)
            self.assertEqual(resumed.record_count(), 1)
            with self.assertRaisesRegex(ValueError, "does not match"):
                resumed.initialize({"configuration_fingerprint": "different"}, resume=True)

    def test_run_store_finalization_is_idempotent_for_same_fingerprint(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            store = RunStore(Path(directory) / "run")
            store.initialize({"configuration_fingerprint": "abc"}, resume=False)
            document = {"configuration_fingerprint": "abc"}
            store.finalize(document, "report\n")
            store.finalize(document, "report\n")
            self.assertTrue(store.results_path.exists())
            self.assertTrue(store.report_path.exists())

    def test_comparison_is_not_estimable_when_no_task_passes(self) -> None:
        records = [
            {
                "condition": condition,
                "task_id": "task-001",
                "seed": 0,
                "passed": False,
                "severe_mutations": 0,
                "total_tokens": 10,
                "model_calls": 1,
                "generated_candidates": 1,
                "constraint_rejections": 0,
                "duration_ms": 1,
            }
            for condition in ("langgraph", "bouncer")
        ]
        summaries = {
            condition: summarize([record for record in records if record["condition"] == condition])
            for condition in ("langgraph", "bouncer")
        }
        manifest = {
            "primary_baseline": "langgraph",
            "bootstrap": {"samples": 10, "confidence": 0.95, "seed": 1},
            "hypotheses": {
                "h1": {
                    "maximum_relative_delta": -0.1,
                    "pass_rate_noninferiority_margin": -0.05,
                },
                "h2": {
                    "minimum_relative_reduction": 0.5,
                    "pass_rate_noninferiority_margin": -0.05,
                },
            },
        }
        comparison = compare_conditions(records, summaries, manifest)
        self.assertIsNone(comparison["relative_token_delta"])
        self.assertFalse(comparison["h1_supported_in_simulation"])
        report = render_report(
            {
                "evaluation_id": "test",
                "generated_at": "2026-07-27T00:00:00Z",
                "duration_seconds": 0,
                "provenance": {
                    "source_revision": "0" * 40,
                    "source_fingerprint_sha256": "0" * 64,
                    "objective_calibration": {"calibration_id": "test"},
                },
                "summaries": {
                    "langgraph": summaries["langgraph"],
                    "structured": summaries["langgraph"],
                    "bouncer": summaries["bouncer"],
                },
                "comparisons": comparison,
            }
        )
        self.assertIn("not estimable", report)


if __name__ == "__main__":
    unittest.main()
