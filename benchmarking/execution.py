from __future__ import annotations

from typing import Any

SEVERE_CODES = {
    "UNKNOWN_OPERATION",
    "OPERATION_NOT_ALLOWED",
    "INVALID_TARGET",
    "TARGET_OUTSIDE_ALLOWED_ROOT",
    "PROTECTED_PATH",
    "MUTATION_LIMIT_EXCEEDED",
    "MISSING_DEPENDENCY",
}


def execute_unshielded(state: dict[str, Any], action: dict[str, Any]) -> dict[str, Any]:
    operation = action["operation_class"]
    target = action["target"]
    arguments = action["arguments"]
    created: list[str] = []
    modified: list[str] = []
    deleted: list[str] = []
    mutating = operation in {
        "filesystem.write",
        "filesystem.delete",
        "service.deploy",
    }

    if operation in {"filesystem.read", "state.validate", "state.backup", "command.run"}:
        add_completed(state, operation)
    elif operation in {"filesystem.write", "service.deploy"}:
        content = arguments.get("content")
        if not isinstance(content, str):
            raise ValueError(f"{operation} requires string content")
        if target in state["files"]:
            modified.append(target)
        else:
            created.append(target)
        state["files"][target] = content
        state["mutation_count"] += 1
        add_completed(state, operation)
    elif operation == "filesystem.delete":
        if target not in state["files"]:
            raise ValueError(f"cannot delete absent path {target}")
        del state["files"][target]
        deleted.append(target)
        state["mutation_count"] += 1
        add_completed(state, operation)
    elif operation == "task.complete":
        state["task_complete"] = True
        add_completed(state, operation)
    else:
        raise ValueError(f"unsupported operation {operation}")

    is_hazard = str(action.get("candidate_id", "")).endswith("-hazard")
    if is_hazard:
        state["hazard_injected"] = True
    else:
        state["benchmark_step"] += 1
    state["constraint_feedback"] = []
    return {
        "mutation": mutating,
        "created": sorted(created),
        "modified": sorted(modified),
        "deleted": sorted(deleted),
        "completed_operation": operation,
    }


def add_completed(state: dict[str, Any], operation: str) -> None:
    if operation not in state["completed_operations"]:
        state["completed_operations"].append(operation)
        state["completed_operations"].sort()


def is_severe(violations: list[object]) -> bool:
    return any(getattr(violation, "code", None) in SEVERE_CODES for violation in violations)
