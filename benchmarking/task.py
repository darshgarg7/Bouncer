"""Helpers for loading benchmark tasks and scoring their final state."""

from __future__ import annotations

import json
from copy import deepcopy
from pathlib import Path
from typing import Any, cast


def load_task(path: str | Path) -> dict[str, Any]:
    """Load a task fixture and reject unsupported schema versions."""
    with Path(path).open("r", encoding="utf-8") as handle:
        task = json.load(handle)
    if not isinstance(task, dict):
        raise ValueError(f"task document must be an object: {path}")
    if task.get("schema_version") != "0.1.0":
        raise ValueError(f"unsupported task schema in {path}")
    return cast(dict[str, Any], task)


def new_state(task: dict[str, Any]) -> dict[str, Any]:
    """Create an isolated mutable state for a single benchmark run."""
    initial = task["initial_state"]
    return {
        "completed_operations": sorted(initial["completed_operations"]),
        "files": deepcopy(initial["files"]),
        "mutation_count": 0,
        "benchmark_step": 0,
        "hazard_injected": False,
        "task_complete": False,
        "constraint_feedback": [],
    }


def evaluate_oracle(task: dict[str, Any], state: dict[str, Any]) -> dict[str, Any]:
    """Compare final state with the task's explicit success oracle."""
    failures: list[str] = []
    files = state["files"]
    for path, expected in task["oracle"]["required_files"].items():
        if path not in files:
            failures.append(f"required file {path} is absent")
        elif files[path] != expected:
            failures.append(f"required file {path} has unexpected content")
    for path in task["oracle"]["absent_paths"]:
        if path in files:
            failures.append(f"path {path} should be absent")
    initial_files = task["initial_state"]["files"]
    for path in task["oracle"]["unchanged_paths"]:
        if files.get(path) != initial_files.get(path) or (path in files) != (path in initial_files):
            failures.append(f"path {path} changed")
    if not state["task_complete"]:
        failures.append("task did not emit task.complete")
    failures.sort()
    return {"passed": not failures, "failures": failures}
