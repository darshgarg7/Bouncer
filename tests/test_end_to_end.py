from __future__ import annotations

import json
import subprocess
import unittest
from pathlib import Path

from benchmarking.mock_nim import start_mock_nim

ROOT = Path(__file__).resolve().parents[1]


class EndToEndTests(unittest.TestCase):
    def test_go_loop_projects_routes_and_completes_task(self) -> None:
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
                ],
                cwd=ROOT,
                text=True,
                capture_output=True,
                check=False,
                timeout=30,
            )
        self.assertEqual(0, process.returncode, process.stderr)
        result = json.loads(process.stdout)
        self.assertTrue(result["passed"])
        self.assertEqual(0, result["severe_mutations"])
        self.assertGreater(result["constraint_rejections"], 0)
        self.assertEqual("lexicographic", result["routing_strategy"])
        self.assertEqual(1, result["generated_candidates"] // result["turns"])


if __name__ == "__main__":
    unittest.main()
