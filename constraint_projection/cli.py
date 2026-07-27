"""Command-line interface for deterministic constraint projection.

The command accepts either one action or a batch. Stream mode keeps a single
projector process alive, which is useful when a benchmark evaluates many small
batches and process startup would dominate the measured latency.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any

from .projector import ProjectionResult, Projector, load_dag


def build_parser() -> argparse.ArgumentParser:
    """Build the argument parser shared by the console script and tests."""
    parser = argparse.ArgumentParser(
        description="Project a Bouncer candidate action through deterministic constraints."
    )
    parser.add_argument(
        "--input",
        default="-",
        help="JSON envelope path, or - for stdin (default)",
    )
    parser.add_argument(
        "--dag",
        default="configs/skill_dag.json",
        help="operation dependency DAG",
    )
    parser.add_argument(
        "--format",
        choices=("xml", "json"),
        default="xml",
        help="output serialization",
    )
    parser.add_argument(
        "--stream",
        action="store_true",
        help="read and write one JSON envelope per line; requires --format json",
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    """Validate arguments, evaluate the input envelope, and print the result."""
    arguments = build_parser().parse_args(argv)
    if arguments.stream and arguments.format != "json":
        print("bouncer-project: --stream requires --format json", file=sys.stderr)
        return 1
    try:
        projector = Projector(load_dag(arguments.dag))
    except (OSError, json.JSONDecodeError, TypeError, ValueError) as error:
        print(f"bouncer-project: {error}", file=sys.stderr)
        return 1

    if arguments.stream:
        return _run_stream(projector)

    try:
        envelope = _load_envelope(arguments.input)
        results = _evaluate_envelope(projector, envelope)
    except (OSError, json.JSONDecodeError, TypeError, ValueError) as error:
        print(f"bouncer-project: {error}", file=sys.stderr)
        return 1

    if len(results) == 1 and "actions" not in envelope:
        print(results[0].to_json() if arguments.format == "json" else results[0].to_xml())
    elif arguments.format == "json":
        print(
            json.dumps(
                {"results": [result.as_dict() for result in results]},
                ensure_ascii=False,
                separators=(",", ":"),
                sort_keys=True,
            )
        )
    else:
        print("\n".join(result.to_xml() for result in results))
    return 0


def _run_stream(projector: Projector) -> int:
    for line in sys.stdin:
        response: dict[str, object]
        try:
            value = json.loads(line)
            if not isinstance(value, dict):
                raise TypeError("input envelope must be a JSON object")
            results = _evaluate_envelope(projector, value)
            response = {"results": [result.as_dict() for result in results]}
        except (json.JSONDecodeError, TypeError, ValueError) as error:
            response = {"error": str(error)}
        print(
            json.dumps(
                response,
                ensure_ascii=False,
                separators=(",", ":"),
                sort_keys=True,
            ),
            flush=True,
        )
    return 0


def _evaluate_envelope(projector: Projector, envelope: dict[str, Any]) -> list[ProjectionResult]:
    state = _require_object(envelope, "state")
    policy = _require_object(envelope, "policy")
    if "actions" in envelope:
        actions = envelope["actions"]
        if not isinstance(actions, list) or any(not isinstance(action, dict) for action in actions):
            raise TypeError("input actions must be an array of JSON objects")
        return [projector.evaluate(action, state, policy) for action in actions]
    action = _require_object(envelope, "action")
    return [projector.evaluate(action, state, policy)]


def _load_envelope(source: str) -> dict[str, Any]:
    if source == "-":
        value = json.load(sys.stdin)
    else:
        with Path(source).open("r", encoding="utf-8") as handle:
            value = json.load(handle)
    if not isinstance(value, dict):
        raise TypeError("input envelope must be a JSON object")
    return value


def _require_object(envelope: dict[str, Any], field: str) -> dict[str, Any]:
    value = envelope.get(field)
    if not isinstance(value, dict):
        raise TypeError(f"input {field} must be a JSON object")
    return value
