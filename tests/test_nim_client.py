from __future__ import annotations

import io
import json
import unittest
from typing import Any
from unittest.mock import patch

from benchmarking.nim_client import ModelConfig, propose


def response_document() -> dict[str, Any]:
    action = {
        "candidate_id": "candidate-1",
        "operation_class": "task.complete",
        "tool": "complete",
        "target": "task",
        "arguments": {},
        "declared_dependencies": [],
        "estimated_objectives": {
            "latency_ms": 1,
            "cost_units": 0.01,
            "safety_risk": 0.01,
        },
    }
    return {
        "choices": [
            {
                "message": {"content": json.dumps(action)},
                "finish_reason": "stop",
            }
        ],
        "usage": {
            "prompt_tokens": 10,
            "completion_tokens": 20,
            "total_tokens": 30,
            "completion_tokens_details": {"reasoning_tokens": 5},
        },
    }


class NIMClientTests(unittest.TestCase):
    def test_hosted_dialect_and_authentication_are_explicit(self) -> None:
        captured: dict[str, Any] = {}

        def fake_open(request: Any, timeout: float) -> io.BytesIO:
            captured["request"] = request
            captured["timeout"] = timeout
            return io.BytesIO(json.dumps(response_document()).encode())

        model = ModelConfig(
            endpoint="https://provider.example/v1",
            model_id="provider-model",
            api_key="secret",
            reasoning_budget=100,
            reasoning_budget_parameter="reasoning_budget",
            max_tokens=200,
            top_p=0.95,
            reasoning_effort="medium",
            timeout_seconds=7,
        )
        with patch("benchmarking.nim_client.urllib.request.urlopen", fake_open):
            result = propose(
                model,
                {
                    "task_id": "task-001",
                    "instruction": "finish",
                    "policy": {
                        "allowed_operation_classes": ["task.complete"],
                        "allowed_path_prefixes": ["workspace/"],
                    },
                },
                {"completed_operations": [], "files": {}},
                42,
                beam=False,
            )

        request = captured["request"]
        payload = json.loads(request.data)
        self.assertEqual("Bearer secret", request.get_header("Authorization"))
        self.assertEqual(100, payload["reasoning_budget"])
        self.assertNotIn("thinking_token_budget", payload)
        self.assertEqual(0.95, payload["top_p"])
        self.assertEqual("medium", payload["reasoning_effort"])
        self.assertTrue(payload["chat_template_kwargs"]["enable_thinking"])
        self.assertEqual(7, captured["timeout"])
        self.assertEqual(30, result.total_tokens)

    def test_model_config_rejects_unknown_budget_parameter(self) -> None:
        model = ModelConfig(
            endpoint="https://provider.example/v1",
            reasoning_budget_parameter="unknown",
        )
        with self.assertRaisesRegex(ValueError, "budget parameter"):
            model.validate()

    def test_model_config_rejects_invalid_hosted_controls(self) -> None:
        with self.assertRaisesRegex(ValueError, "top_p"):
            ModelConfig(endpoint="https://provider.example/v1", top_p=1.01).validate()
        with self.assertRaisesRegex(ValueError, "reasoning effort"):
            ModelConfig(
                endpoint="https://provider.example/v1", reasoning_effort="balanced"
            ).validate()


if __name__ == "__main__":
    unittest.main()
