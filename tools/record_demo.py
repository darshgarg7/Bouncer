#!/usr/bin/env python3
"""Record a concise, reproducible Bouncer terminal demo as an asciinema cast."""

from __future__ import annotations

import json
import subprocess
import tempfile
from pathlib import Path
from typing import Any

from benchmarking.mock_nim import start_mock_nim
from benchmarking.provenance import ROOT


def run(arguments: list[str]) -> str:
    """Run one local demo command and return its standard output."""
    process = subprocess.run(
        arguments,
        cwd=ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    return process.stdout.strip()


def compact_json(value: object) -> str:
    """Render stable, readable terminal JSON."""
    return json.dumps(value, indent=2, sort_keys=True)


def main() -> None:
    """Execute the local path and create a 75-second edited playback."""
    output_path = ROOT / "docs/demo.cast"
    # Claim the final path before doing work so regeneration cannot silently
    # overwrite an existing recording. The placeholder also lets the link
    # audit verify the README while this first recording is being assembled.
    with output_path.open("x", encoding="utf-8") as handle:
        handle.write("{}\n")
    audit = run([str(ROOT / ".venv/bin/python"), "tools/release_audit.py"])
    projection = run(
        [
            str(ROOT / ".venv/bin/python"),
            "-m",
            "constraint_projection",
            "--input",
            "examples/projection-input.json",
            "--format",
            "json",
        ]
    )
    subprocess.run(
        ["go", "build", "-o", "bin/bouncer-run", "./cmd/bouncer-run"],
        cwd=ROOT,
        check=True,
    )
    subprocess.run(
        ["go", "build", "-o", "bin/bouncer-verify-log", "./cmd/bouncer-verify-log"],
        cwd=ROOT,
        check=True,
    )
    with (
        tempfile.TemporaryDirectory() as directory,
        start_mock_nim(ROOT / "benchmarks/scenarios.json") as server,
    ):
        temporary = Path(directory)
        result_path = temporary / "result.json"
        event_path = temporary / "events.jsonl"
        run(
            [
                str(ROOT / "bin/bouncer-run"),
                "-endpoint",
                server.endpoint,
                "-task",
                "benchmarks/tasks/task-001.json",
                "-event-log",
                str(event_path),
                "-output",
                str(result_path),
            ]
        )
        result = json.loads(result_path.read_text(encoding="utf-8"))
        verification = json.loads(
            run([str(ROOT / "bin/bouncer-verify-log"), "-event-log", str(event_path)])
        )

    run_summary = {
        "passed": result["passed"],
        "turns": result["turns"],
        "model_calls": result["model_calls"],
        "constraint_rejections": result["constraint_rejections"],
        "executed_actions": result["executed_actions"],
        "calibration_id": result["objective_calibration"]["calibration_id"],
        "model_influence": result["objective_calibration"]["model_influence"],
    }
    pilot = json.loads(
        (ROOT / "benchmarks/reports/nvidia-hosted-pilot-2026-07-27/summary.json").read_text(
            encoding="utf-8"
        )
    )
    pilot_summary = {
        "attempts": pilot["attempts"],
        "successes": pilot["successes"],
        "constraint_rejections": pilot["constraint_rejections"],
        "severe_mutations": pilot["severe_mutations"],
        "claim": pilot["scope"]["claim"],
    }

    events: list[list[Any]] = [
        [0.2, "o", "Bouncer — deterministic control boundaries for model-proposed actions\r\n"],
        [2.5, "o", "$ make release-audit\r\n"],
        [4.0, "o", audit + "\r\n"],
        [9.0, "o", "\r\n1) The model proposes; deterministic policy authorizes.\r\n"],
        [12.0, "o", "$ make project-example\r\n"],
        [14.0, "o", projection + "\r\n"],
        [
            21.0,
            "o",
            "Rejected: filesystem.write was missing filesystem.read. Nothing executed.\r\n",
        ],
        [27.0, "o", "\r\n2) Run the complete typed loop against the deterministic provider.\r\n"],
        [
            30.0,
            "o",
            "$ bin/bouncer-run -task benchmarks/tasks/task-001.json "
            "-event-log /tmp/events.jsonl\r\n",
        ],
        [34.0, "o", compact_json(run_summary) + "\r\n"],
        [45.0, "o", "\r\n3) Verify lifecycle completeness and the terminal chain hash.\r\n"],
        [48.0, "o", "$ bin/bouncer-verify-log -event-log /tmp/events.jsonl\r\n"],
        [50.0, "o", compact_json(verification) + "\r\n"],
        [59.0, "o", "\r\n4) Keep hosted evidence honest.\r\n"],
        [
            62.0,
            "o",
            "$ jq '{attempts,successes,constraint_rejections,severe_mutations,scope}' "
            "pilot/summary.json\r\n",
        ],
        [65.0, "o", compact_json(pilot_summary) + "\r\n"],
        [73.0, "o", "\r\n2/3 is connectivity evidence—not an effectiveness claim.\r\n"],
        [75.0, "o", "Policy decides. Evidence stays reproducible.\r\n"],
    ]
    header = {
        "version": 2,
        "width": 108,
        "height": 32,
        "timestamp": 1785132000,
        "duration": 75.0,
        "idle_time_limit": 8.0,
        "command": "tools/record_demo.py",
        "title": "Bouncer: proposal, policy, execution, and evidence in 75 seconds",
        "env": {"SHELL": "/bin/zsh", "TERM": "xterm-256color"},
    }
    with output_path.open("w", encoding="utf-8") as handle:
        handle.write(json.dumps(header, separators=(",", ":")) + "\n")
        for event in events:
            handle.write(json.dumps(event, separators=(",", ":")) + "\n")
    print(output_path)


if __name__ == "__main__":
    main()
