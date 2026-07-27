from __future__ import annotations

import hashlib
import json
import subprocess
import tempfile
import unittest
from pathlib import Path

from benchmarking.learning.observations import build_trajectory, read_events
from benchmarking.learning.outcomes import train_artifact
from benchmarking.mock_nim import start_mock_nim

ROOT = Path(__file__).resolve().parents[1]


class EndToEndTests(unittest.TestCase):
    def test_go_loop_projects_routes_and_completes_task(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            event_log = Path(temporary) / "events.jsonl"
            with start_mock_nim(ROOT / "benchmarks/scenarios.json") as server:
                process = subprocess.run(
                    [
                        "go",
                        "run",
                        "./cmd/bouncer-run",
                        "-endpoint",
                        server.endpoint,
                        "-task",
                        "benchmarks/tasks/task-001.json",
                        "-project-root",
                        str(ROOT),
                        "-seed",
                        "2",
                        "-learning-mode",
                        "shadow",
                        "-learning-risk-ceiling",
                        "1",
                        "-learning-max-relative-uncertainty",
                        "10",
                        "-event-log",
                        str(event_log),
                    ],
                    cwd=ROOT,
                    text=True,
                    capture_output=True,
                    check=False,
                    timeout=60,
                )
            self.assertEqual(0, process.returncode, process.stderr)
            trajectory = build_trajectory(read_events(event_log))
            dataset = (json.dumps(trajectory, sort_keys=True) + "\n").encode()
            artifact, _ = train_artifact(
                [trajectory],
                dataset_sha256=hashlib.sha256(dataset).hexdigest(),
                artifact_id="end-to-end-trained",
                validation_fraction=0,
            )
            artifact_path = Path(temporary) / "trained-artifact.json"
            artifact_path.write_text(json.dumps(artifact), encoding="utf-8")
            with start_mock_nim(ROOT / "benchmarks/scenarios.json") as active_server:
                active_process = subprocess.run(
                    [
                        "go",
                        "run",
                        "./cmd/bouncer-run",
                        "-endpoint",
                        active_server.endpoint,
                        "-task",
                        "benchmarks/tasks/task-001.json",
                        "-project-root",
                        str(ROOT),
                        "-seed",
                        "2",
                        "-learning-mode",
                        "active",
                        "-learning-artifact",
                        str(artifact_path),
                        "-learning-risk-ceiling",
                        "1",
                        "-learning-max-relative-uncertainty",
                        "10",
                    ],
                    cwd=ROOT,
                    text=True,
                    capture_output=True,
                    check=False,
                    timeout=60,
                )
            self.assertEqual(0, active_process.returncode, active_process.stderr)
        result = json.loads(process.stdout)
        self.assertTrue(result["passed"])
        self.assertEqual(0, result["severe_mutations"])
        self.assertGreater(result["constraint_rejections"], 0)
        self.assertEqual("lexicographic", result["routing_strategy"])
        self.assertEqual("shadow", result["learning_mode"])
        self.assertEqual(1, result["generated_candidates"] // result["turns"])
        self.assertTrue(trajectory["passed"])
        self.assertGreater(len(trajectory["transitions"]), 0)
        self.assertIsNone(trajectory["transitions"][0]["outcome"]["cost_units"])
        active_result = json.loads(active_process.stdout)
        self.assertTrue(active_result["passed"])
        self.assertEqual("learned_pareto_safety_first", active_result["routing_strategy"])


if __name__ == "__main__":
    unittest.main()
