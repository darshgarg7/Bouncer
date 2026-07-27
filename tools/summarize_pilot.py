#!/usr/bin/env python3
"""Validate and summarize a three-task hosted-provider pilot directory."""

from __future__ import annotations

import argparse
import json
import subprocess
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, cast

from benchmarking.provenance import ROOT, source_fingerprint, source_revision

TASK_IDS = ("task-001", "task-002", "task-003")


def load_object(path: Path) -> dict[str, Any]:
    """Load one JSON object and reject all other top-level values."""
    value = cast(object, json.loads(path.read_text(encoding="utf-8")))
    if not isinstance(value, dict):
        raise ValueError(f"expected a JSON object: {path}")
    return cast(dict[str, Any], value)


def summarize(directory: Path) -> dict[str, Any]:
    """Verify each task artifact and build one publication summary."""
    results: list[dict[str, Any]] = []
    anchors: dict[str, dict[str, Any]] = {}
    outcomes: dict[str, dict[str, Any]] = {}
    started_payload: dict[str, Any] | None = None
    calibration: dict[str, Any] | None = None
    for task_id in TASK_IDS:
        result = load_object(directory / f"{task_id}-result.json")
        if result.get("task_id") != task_id:
            raise ValueError(f"result task mismatch for {task_id}")
        events_path = directory / f"{task_id}-events.jsonl"
        verification_path = directory / f"{task_id}-verification.json"
        recorded_verification = load_object(verification_path)
        process = subprocess.run(
            [str(ROOT / "bin/bouncer-verify-log"), "-event-log", str(events_path)],
            cwd=ROOT,
            check=True,
            capture_output=True,
            text=True,
        )
        current_verification = cast(object, json.loads(process.stdout))
        if current_verification != recorded_verification:
            raise ValueError(f"verification artifact is stale for {task_id}")
        if recorded_verification.get("task_id") != task_id:
            raise ValueError(f"verification task mismatch for {task_id}")
        if recorded_verification.get("terminal_event") != "run.completed":
            raise ValueError(f"pilot task did not end in run.completed: {task_id}")
        final_hash = recorded_verification.get("final_hash")
        if not isinstance(final_hash, str) or len(final_hash) != 64:
            raise ValueError(f"missing terminal hash for {task_id}")
        lines = [
            cast(dict[str, Any], json.loads(line))
            for line in events_path.read_text(encoding="utf-8").splitlines()
            if line.strip()
        ]
        if not lines or lines[0].get("event_type") != "run.started":
            raise ValueError(f"missing run.started for {task_id}")
        payload = lines[0].get("payload")
        if not isinstance(payload, dict):
            raise ValueError(f"invalid run.started payload for {task_id}")
        if started_payload is None:
            started_payload = payload
        for field in ("model", "provider", "endpoint"):
            if payload.get(field) != started_payload.get(field):
                raise ValueError(f"inconsistent {field} across pilot tasks")
        result_calibration = result.get("objective_calibration")
        if not isinstance(result_calibration, dict):
            raise ValueError(f"missing objective calibration for {task_id}")
        if calibration is None:
            calibration = result_calibration
        elif result_calibration != calibration:
            raise ValueError("pilot tasks used inconsistent objective calibration")
        anchors[task_id] = {
            "run_id": recorded_verification.get("run_id"),
            "events": recorded_verification.get("events"),
            "terminal_event": recorded_verification.get("terminal_event"),
            "final_hash": final_hash,
        }
        outcomes[task_id] = {
            "passed": bool(result.get("passed")),
            "task_complete": bool(result.get("task_complete")),
            "oracle_failures": result.get("oracle_failures", []),
            "turns": int(result.get("turns", 0)),
            "model_calls": int(result.get("model_calls", 0)),
            "total_tokens": int(result.get("total_tokens", 0)),
            "constraint_rejections": int(result.get("constraint_rejections", 0)),
            "executed_actions": int(result.get("executed_actions", 0)),
        }
        results.append(result)
    if started_payload is None or calibration is None:
        raise ValueError("pilot did not contain any tasks")
    influence = calibration.get("model_influence")
    if influence != {"latency_ms": 0, "cost_units": 0, "safety_risk": 0}:
        raise ValueError("published pilot must use the zero-influence bootstrap artifact")
    return {
        "schema_version": "0.1.0",
        "pilot_id": directory.name,
        "generated_at": datetime.now(UTC).isoformat(),
        "source": {
            "revision": source_revision(),
            "fingerprint_sha256": source_fingerprint(),
        },
        "provider": started_payload.get("provider"),
        "model": started_payload.get("model"),
        "endpoint": started_payload.get("endpoint"),
        "task_ids": list(TASK_IDS),
        "attempts": len(results),
        "successes": sum(bool(result.get("passed")) for result in results),
        "task_completions": sum(bool(result.get("task_complete")) for result in results),
        "constraint_rejections": sum(
            int(result.get("constraint_rejections", 0)) for result in results
        ),
        "severe_mutations": sum(int(result.get("severe_mutations", 0)) for result in results),
        "total_model_calls": sum(int(result.get("model_calls", 0)) for result in results),
        "total_tokens": sum(int(result.get("total_tokens", 0)) for result in results),
        "objective_calibration": calibration,
        "task_outcomes": outcomes,
        "event_chain_anchors": anchors,
        "scope": {
            "tasks": "three authored deterministic virtual-state tasks",
            "provider": "one hosted model, one seed, no comparison baseline",
            "claim": "connectivity and control-loop compatibility only; not effectiveness",
            "anchor_limit": (
                "terminal hashes are checked-in anchors, not signatures or independently "
                "timestamped external attestations"
            ),
        },
    }


def render_report(summary: dict[str, Any]) -> str:
    """Render a concise human-readable pilot report."""
    calibration = cast(dict[str, Any], summary["objective_calibration"])
    source = cast(dict[str, Any], summary["source"])
    outcomes = cast(dict[str, dict[str, Any]], summary["task_outcomes"])
    outcome_lines = []
    for task_id in TASK_IDS:
        outcome = outcomes[task_id]
        status = "pass" if outcome["passed"] else "fail"
        detail = ""
        if outcome["oracle_failures"]:
            detail = " — " + "; ".join(str(item) for item in outcome["oracle_failures"])
        outcome_lines.append(f"- `{task_id}`: **{status}**{detail}")
    lines = [
        "# NVIDIA hosted-provider pilot",
        "",
        f"**Pilot:** `{summary['pilot_id']}`",
        f"**Generated:** {summary['generated_at']}",
        f"**Source revision:** `{source['revision']}`",
        f"**Source fingerprint:** `{source['fingerprint_sha256']}`",
        f"**Model:** `{summary['model']}`",
        f"**Objective artifact:** `{calibration['calibration_id']}`",
        "",
        "> This is connectivity and control-loop compatibility evidence, not evidence of "
        "model effectiveness, comparative quality, or production safety.",
        "",
        "## Result",
        "",
        f"- {summary['successes']}/{summary['attempts']} authored virtual tasks passed;",
        f"- {summary['constraint_rejections']} proposals were rejected before execution;",
        f"- {summary['severe_mutations']} severe virtual mutations were observed;",
        f"- {summary['total_model_calls']} hosted model calls used "
        f"{summary['total_tokens']:,} provider-reported tokens; and",
        "- all three lifecycle chains verify against the terminal hashes in `summary.json`.",
        "",
        "### Per-task outcomes",
        "",
        *outcome_lines,
        "",
        "The bootstrap objective artifact gives model-authored latency, cost, and risk "
        "values zero routing influence. Each event chain begins with `run.started`, ends "
        "with `run.completed`, and has a stable run/task identity.",
        "",
        "## Evidence boundary",
        "",
        "These are three deterministic repository fixtures, one model, one seed, a virtual "
        "executor, and no baseline. The checked-in terminal hashes detect accidental or "
        "partial tampering relative to this repository state, but they are not signatures "
        "and are not independently timestamped external anchors.",
        "",
        "Machine-readable totals, calibration metadata, source provenance, and per-task "
        "terminal hashes are in [`summary.json`](summary.json).",
        "",
    ]
    return "\n".join(lines)


def write_exclusive(path: Path, content: str) -> None:
    """Create one artifact without overwriting an existing pilot result."""
    with path.open("x", encoding="utf-8") as handle:
        handle.write(content)


def write_summary(path: Path, content: str, *, refresh: bool) -> None:
    """Write a derived summary, optionally refreshing only an existing summary."""
    if refresh:
        if not path.is_file():
            raise ValueError(f"cannot refresh missing summary: {path}")
        path.write_text(content, encoding="utf-8")
        return
    write_exclusive(path, content)


def main() -> None:
    """Validate one directory and create its JSON and Markdown summaries."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument(
        "--refresh",
        action="store_true",
        help="refresh existing derived summary files without changing raw task artifacts",
    )
    args = parser.parse_args()
    previous_source = None
    if args.refresh:
        previous = load_object(args.directory / "summary.json")
        previous_source = previous.get("source")
    summary = summarize(args.directory)
    if isinstance(previous_source, dict):
        if previous_source.get("fingerprint_sha256") != summary["source"]["fingerprint_sha256"]:
            raise ValueError("cannot refresh a pilot after execution-source changes")
        summary["source"] = previous_source
    write_summary(
        args.directory / "summary.json",
        json.dumps(summary, indent=2) + "\n",
        refresh=args.refresh,
    )
    write_summary(args.directory / "README.md", render_report(summary), refresh=args.refresh)
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()
