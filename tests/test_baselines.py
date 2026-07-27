from __future__ import annotations

import unittest
from pathlib import Path

from benchmarking.baselines import run_baseline
from benchmarking.mock_nim import start_mock_nim

ROOT = Path(__file__).resolve().parents[1]


class BaselineTests(unittest.TestCase):
    def test_langgraph_baseline_completes_task_and_records_hazard(self) -> None:
        with start_mock_nim(ROOT / "benchmarks/scenarios.json") as server:
            result = run_baseline(
                str(ROOT / "benchmarks/tasks/task-001.json"),
                seed=2,
                endpoint=server.endpoint,
                condition="langgraph",
                dag_path=str(ROOT / "configs/skill_dag.json"),
            )
        self.assertTrue(result["passed"])
        self.assertGreaterEqual(result["severe_mutations"], 1)
        self.assertGreater(result["total_tokens"], 0)

    def test_structured_baseline_uses_five_candidate_beam(self) -> None:
        with start_mock_nim(ROOT / "benchmarks/scenarios.json") as server:
            result = run_baseline(
                str(ROOT / "benchmarks/tasks/task-001.json"),
                seed=0,
                endpoint=server.endpoint,
                condition="structured",
                dag_path=str(ROOT / "configs/skill_dag.json"),
            )
        self.assertTrue(result["passed"])
        self.assertEqual(result["turns"] * 5, result["generated_candidates"])


if __name__ == "__main__":
    unittest.main()
