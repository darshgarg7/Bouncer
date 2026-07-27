from __future__ import annotations

import json
import unittest
import urllib.request
from pathlib import Path

from benchmarking.mock_nim import start_mock_nim

ROOT = Path(__file__).resolve().parents[1]


class MockNIMTests(unittest.TestCase):
    def test_beam_and_single_action_share_policy(self) -> None:
        state = {
            "completed_operations": [],
            "files": {},
            "benchmark_step": 0,
            "hazard_injected": False,
        }
        with start_mock_nim(ROOT / "benchmarks/scenarios.json") as server:
            beam = self.request(server.endpoint, "Return exactly five actions", state, 2)
            single = self.request(server.endpoint, "Return exactly one JSON action", state, 2)
        beam_content = json.loads(beam["choices"][0]["message"]["content"])
        single_content = json.loads(single["choices"][0]["message"]["content"])
        self.assertEqual(5, len(beam_content["actions"]))
        self.assertEqual(beam_content["actions"][0], single_content)
        self.assertEqual("stop", beam["choices"][0]["finish_reason"])
        self.assertGreater(beam["usage"]["total_tokens"], single["usage"]["total_tokens"])

    @staticmethod
    def request(
        endpoint: str, system: str, state: dict[str, object], seed: int
    ) -> dict[str, object]:
        payload = json.dumps(
            {
                "model": "mock",
                "messages": [
                    {"role": "system", "content": system},
                    {
                        "role": "user",
                        "content": "Task ID: task-001\nInstruction: test\nTyped state JSON:\n"
                        + json.dumps(state),
                    },
                ],
                "seed": seed,
            }
        ).encode()
        request = urllib.request.Request(
            endpoint + "/chat/completions",
            data=payload,
            headers={"Content-Type": "application/json"},
        )
        with urllib.request.urlopen(request, timeout=2) as response:
            return json.load(response)


if __name__ == "__main__":
    unittest.main()
