from __future__ import annotations

import json
import subprocess
import sys
import unittest
from pathlib import Path

from constraint_projection import DAGConfigurationError, Projector, load_dag

ROOT = Path(__file__).resolve().parents[1]


def valid_action(**overrides: object) -> dict[str, object]:
    action: dict[str, object] = {
        "candidate_id": "agent-1-candidate-1",
        "operation_class": "filesystem.write",
        "tool": "apply_patch",
        "target": "workspace/service/config.yaml",
        "arguments": {"patch_content": "fixture"},
        "declared_dependencies": ["filesystem.read"],
        "estimated_objectives": {
            "latency_ms": 10,
            "cost_units": 0.01,
            "safety_risk": 0.05,
        },
    }
    action.update(overrides)
    return action


def valid_policy() -> dict[str, object]:
    return {
        "allowed_operation_classes": [
            "filesystem.read",
            "filesystem.write",
            "state.validate",
        ],
        "allowed_path_prefixes": ["workspace/service/"],
        "protected_paths": ["workspace/service/secrets.env"],
        "denied_read_paths": ["workspace/service/private/"],
        "max_mutations": 1,
    }


class ProjectorTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.projector = Projector(load_dag(ROOT / "configs/skill_dag.json"))

    def test_allows_action_after_dependency(self) -> None:
        result = self.projector.evaluate(
            valid_action(),
            {"completed_operations": ["filesystem.read"], "files": {}},
            valid_policy(),
        )
        self.assertTrue(result.allowed)
        self.assertEqual(
            '<constraint_pass action_id="agent-1-candidate-1"/>',
            result.to_xml(),
        )

    def test_missing_dependency_is_canonical(self) -> None:
        result = self.projector.evaluate(
            valid_action(),
            {"completed_operations": [], "files": {}},
            valid_policy(),
        )
        self.assertFalse(result.allowed)
        self.assertEqual(
            '<constraint_violation action_id="agent-1-candidate-1" '
            'code="MISSING_DEPENDENCY" operation="filesystem.write" '
            'dependency="filesystem.read"/>',
            result.to_xml(),
        )

    def test_rejects_path_traversal(self) -> None:
        result = self.projector.evaluate(
            valid_action(target="workspace/service/../../outside.txt"),
            {"completed_operations": ["filesystem.read"]},
            valid_policy(),
        )
        self.assertEqual("INVALID_TARGET", result.violations[0].code)

    def test_rejects_outside_root(self) -> None:
        result = self.projector.evaluate(
            valid_action(target="workspace/other/config.yaml"),
            {"completed_operations": ["filesystem.read"]},
            valid_policy(),
        )
        self.assertEqual("TARGET_OUTSIDE_ALLOWED_ROOT", result.violations[0].code)

    def test_rejects_mutation_of_protected_path(self) -> None:
        result = self.projector.evaluate(
            valid_action(target="workspace/service/secrets.env"),
            {"completed_operations": ["filesystem.read"]},
            valid_policy(),
        )
        self.assertEqual("PROTECTED_PATH", result.violations[0].code)

    def test_rejects_mutation_after_quota_is_exhausted(self) -> None:
        result = self.projector.evaluate(
            valid_action(),
            {
                "completed_operations": ["filesystem.read"],
                "mutation_count": 1,
            },
            valid_policy(),
        )
        self.assertFalse(result.allowed)
        self.assertEqual("MUTATION_LIMIT_EXCEEDED", result.violations[0].code)
        self.assertIn('current="1" maximum="1"', result.to_xml())

    def test_read_of_protected_path_is_not_treated_as_mutation(self) -> None:
        result = self.projector.evaluate(
            valid_action(
                operation_class="filesystem.read",
                tool="read_file",
                target="workspace/service/secrets.env",
                declared_dependencies=[],
            ),
            {"completed_operations": []},
            valid_policy(),
        )
        self.assertTrue(result.allowed)

    def test_explicit_read_deny_is_enforced(self) -> None:
        result = self.projector.evaluate(
            valid_action(
                operation_class="filesystem.read",
                tool="read_file",
                target="workspace/service/private/token.txt",
                declared_dependencies=[],
            ),
            {"completed_operations": []},
            valid_policy(),
        )
        self.assertFalse(result.allowed)
        self.assertEqual("READ_DENIED", result.violations[0].code)

    def test_unknown_operation_is_rejected(self) -> None:
        result = self.projector.evaluate(
            valid_action(operation_class="shell.root"),
            {"completed_operations": []},
            valid_policy(),
        )
        self.assertEqual("UNKNOWN_OPERATION", result.violations[0].code)

    def test_xml_escapes_structured_values(self) -> None:
        result = self.projector.evaluate(
            valid_action(candidate_id='agent-1"bad'),
            {"completed_operations": []},
            valid_policy(),
        )
        self.assertEqual("unknown", result.action_id)
        self.assertIn('field="candidate_id"', result.to_xml())

    def test_replay_is_byte_identical(self) -> None:
        action = valid_action(target="workspace/other/config.yaml")
        state = {"completed_operations": []}
        first = self.projector.evaluate(action, state, valid_policy()).to_xml()
        for _ in range(100):
            self.assertEqual(
                first,
                self.projector.evaluate(action, state, valid_policy()).to_xml(),
            )

    def test_cycle_is_rejected(self) -> None:
        with self.assertRaises(DAGConfigurationError):
            Projector({"a": ["b"], "b": ["a"]})

    def test_cli_json_output(self) -> None:
        envelope = {
            "action": valid_action(),
            "state": {"completed_operations": ["filesystem.read"]},
            "policy": valid_policy(),
        }
        process = subprocess.run(
            [
                sys.executable,
                "-m",
                "constraint_projection",
                "--dag",
                "configs/skill_dag.json",
                "--format",
                "json",
            ],
            cwd=ROOT,
            input=json.dumps(envelope),
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(0, process.returncode, process.stderr)
        output = json.loads(process.stdout)
        self.assertTrue(output["allowed"])

    def test_cli_batch_output_preserves_action_order(self) -> None:
        envelope = {
            "actions": [
                valid_action(candidate_id="candidate-a"),
                valid_action(
                    candidate_id="candidate-b",
                    target="workspace/outside/file.txt",
                ),
            ],
            "state": {"completed_operations": ["filesystem.read"]},
            "policy": valid_policy(),
        }
        process = subprocess.run(
            [
                sys.executable,
                "-m",
                "constraint_projection",
                "--dag",
                "configs/skill_dag.json",
                "--format",
                "json",
            ],
            cwd=ROOT,
            input=json.dumps(envelope),
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(0, process.returncode, process.stderr)
        results = json.loads(process.stdout)["results"]
        self.assertEqual(["candidate-a", "candidate-b"], [item["action_id"] for item in results])
        self.assertTrue(results[0]["allowed"])
        self.assertFalse(results[1]["allowed"])

    def test_cli_stream_reuses_projector_and_isolates_bad_envelopes(self) -> None:
        envelope = {
            "actions": [valid_action(candidate_id="candidate-a")],
            "state": {"completed_operations": ["filesystem.read"]},
            "policy": valid_policy(),
        }
        process = subprocess.run(
            [
                sys.executable,
                "-m",
                "constraint_projection",
                "--dag",
                "configs/skill_dag.json",
                "--format",
                "json",
                "--stream",
            ],
            cwd=ROOT,
            input="not-json\n" + json.dumps(envelope) + "\n" + json.dumps(envelope) + "\n",
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(0, process.returncode, process.stderr)
        responses = [json.loads(line) for line in process.stdout.splitlines()]
        self.assertIn("error", responses[0])
        self.assertTrue(responses[1]["results"][0]["allowed"])
        self.assertEqual(responses[1], responses[2])


if __name__ == "__main__":
    unittest.main()
