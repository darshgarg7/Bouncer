from __future__ import annotations

import json
import math
import re
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path, PurePosixPath
from typing import Any
from xml.sax.saxutils import quoteattr

ACTION_FIELDS = frozenset(
    {
        "candidate_id",
        "operation_class",
        "tool",
        "target",
        "arguments",
        "declared_dependencies",
        "estimated_objectives",
    }
)
OBJECTIVE_FIELDS = frozenset({"latency_ms", "cost_units", "safety_risk"})
CANDIDATE_ID_PATTERN = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$")

CODE_PRIORITY = {
    "INVALID_ACTION": 0,
    "UNKNOWN_OPERATION": 1,
    "OPERATION_NOT_ALLOWED": 2,
    "INVALID_TARGET": 3,
    "TARGET_OUTSIDE_ALLOWED_ROOT": 4,
    "PROTECTED_PATH": 5,
    "MUTATION_LIMIT_EXCEEDED": 6,
    "MISSING_DEPENDENCY": 7,
}

DETAIL_ORDER = ("field", "operation", "dependency", "target", "current", "maximum")
MUTATING_OPERATIONS = frozenset({"filesystem.write", "filesystem.delete", "service.deploy"})


class DAGConfigurationError(ValueError):
    """Raised when the declared operation graph is malformed or cyclic."""


@dataclass(frozen=True)
class ConstraintViolation:
    code: str
    details: tuple[tuple[str, str], ...] = ()

    @classmethod
    def create(cls, code: str, **details: object) -> ConstraintViolation:
        ordered: list[tuple[str, str]] = []
        for key in DETAIL_ORDER:
            if key in details:
                ordered.append((key, str(details[key])))
        for key in sorted(set(details).difference(DETAIL_ORDER)):
            ordered.append((key, str(details[key])))
        return cls(code=code, details=tuple(ordered))

    def sort_key(self) -> tuple[object, ...]:
        return (CODE_PRIORITY.get(self.code, 999), self.code, self.details)

    def as_dict(self) -> dict[str, object]:
        return {"code": self.code, "details": dict(self.details)}


@dataclass(frozen=True)
class ProjectionResult:
    action_id: str
    violations: tuple[ConstraintViolation, ...]

    @property
    def allowed(self) -> bool:
        return not self.violations

    def to_xml(self) -> str:
        if self.allowed:
            return f"<constraint_pass action_id={quoteattr(self.action_id)}/>"
        lines: list[str] = []
        for violation in self.violations:
            attributes = [
                f"action_id={quoteattr(self.action_id)}",
                f"code={quoteattr(violation.code)}",
            ]
            attributes.extend(f"{key}={quoteattr(value)}" for key, value in violation.details)
            lines.append(f"<constraint_violation {' '.join(attributes)}/>")
        return "\n".join(lines)

    def to_json(self) -> str:
        return json.dumps(
            self.as_dict(),
            ensure_ascii=False,
            separators=(",", ":"),
            sort_keys=True,
        )

    def as_dict(self) -> dict[str, object]:
        return {
            "action_id": self.action_id,
            "allowed": self.allowed,
            "projection": self.to_xml(),
            "violations": [violation.as_dict() for violation in self.violations],
        }


class Projector:
    def __init__(self, operations: Mapping[str, Sequence[str]]) -> None:
        self._operations = validate_dag(operations)

    def evaluate(
        self,
        action: Mapping[str, Any],
        state: Mapping[str, Any],
        policy: Mapping[str, Any],
    ) -> ProjectionResult:
        action_id = _safe_action_id(action)
        malformed = _validate_action_shape(action)
        if malformed:
            malformed_results = tuple(sorted(malformed, key=ConstraintViolation.sort_key))
            return ProjectionResult(
                action_id=action_id,
                violations=malformed_results,
            )

        operation = str(action["operation_class"])
        target = str(action["target"])
        violations: list[ConstraintViolation] = []

        if operation not in self._operations:
            violations.append(
                ConstraintViolation.create(
                    "UNKNOWN_OPERATION",
                    operation=operation,
                )
            )
        else:
            allowed_operations = _string_set(
                policy.get("allowed_operation_classes"),
                "policy.allowed_operation_classes",
            )
            if operation not in allowed_operations:
                violations.append(
                    ConstraintViolation.create(
                        "OPERATION_NOT_ALLOWED",
                        operation=operation,
                    )
                )

        normalized_target = _normalize_virtual_path(target)
        if normalized_target is None:
            violations.append(ConstraintViolation.create("INVALID_TARGET", target=target))
        else:
            allowed_prefixes = _string_sequence(
                policy.get("allowed_path_prefixes"),
                "policy.allowed_path_prefixes",
            )
            if not any(_path_is_within(normalized_target, prefix) for prefix in allowed_prefixes):
                violations.append(
                    ConstraintViolation.create(
                        "TARGET_OUTSIDE_ALLOWED_ROOT",
                        target=normalized_target,
                    )
                )

            protected_paths = _string_sequence(
                policy.get("protected_paths", []),
                "policy.protected_paths",
            )
            if operation != "filesystem.read" and any(
                _path_is_within(normalized_target, protected) for protected in protected_paths
            ):
                violations.append(
                    ConstraintViolation.create(
                        "PROTECTED_PATH",
                        target=normalized_target,
                    )
                )

        if operation in self._operations:
            if operation in MUTATING_OPERATIONS:
                current_mutations = _nonnegative_int(
                    state.get("mutation_count", 0),
                    "state.mutation_count",
                )
                maximum_mutations = _nonnegative_int(
                    policy.get("max_mutations"),
                    "policy.max_mutations",
                )
                if current_mutations >= maximum_mutations:
                    violations.append(
                        ConstraintViolation.create(
                            "MUTATION_LIMIT_EXCEEDED",
                            operation=operation,
                            current=current_mutations,
                            maximum=maximum_mutations,
                        )
                    )
            completed = _string_set(
                state.get("completed_operations", []),
                "state.completed_operations",
            )
            for dependency in self._operations[operation]:
                if dependency not in completed:
                    violations.append(
                        ConstraintViolation.create(
                            "MISSING_DEPENDENCY",
                            operation=operation,
                            dependency=dependency,
                        )
                    )

        unique = {violation: None for violation in violations}
        ordered = tuple(sorted(unique, key=ConstraintViolation.sort_key))
        return ProjectionResult(action_id=action_id, violations=ordered)


def load_dag(path: str | Path) -> dict[str, tuple[str, ...]]:
    source = Path(path)
    with source.open("r", encoding="utf-8") as handle:
        document = json.load(handle)
    if not isinstance(document, dict) or document.get("schema_version") != "0.1.0":
        raise DAGConfigurationError("DAG schema_version must be 0.1.0")
    operations = document.get("operations")
    if not isinstance(operations, dict):
        raise DAGConfigurationError("DAG operations must be an object")
    return validate_dag(operations)


def validate_dag(
    operations: Mapping[str, Sequence[str]],
) -> dict[str, tuple[str, ...]]:
    normalized: dict[str, tuple[str, ...]] = {}
    for operation, dependencies in operations.items():
        if not isinstance(operation, str) or not operation:
            raise DAGConfigurationError("operation names must be non-empty strings")
        if isinstance(dependencies, str | bytes) or not isinstance(dependencies, Sequence):
            raise DAGConfigurationError(f"dependencies for {operation!r} must be an array")
        values: list[str] = []
        for dependency in dependencies:
            if not isinstance(dependency, str) or not dependency:
                raise DAGConfigurationError(
                    f"dependency names for {operation!r} must be non-empty strings"
                )
            values.append(dependency)
        if len(values) != len(set(values)):
            raise DAGConfigurationError(f"dependencies for {operation!r} contain duplicates")
        normalized[operation] = tuple(sorted(values))

    for operation, dependencies in normalized.items():
        for dependency in dependencies:
            if dependency not in normalized:
                raise DAGConfigurationError(
                    f"operation {operation!r} references unknown dependency {dependency!r}"
                )

    visiting: set[str] = set()
    visited: set[str] = set()

    def visit(operation: str) -> None:
        if operation in visiting:
            raise DAGConfigurationError(f"cycle detected at operation {operation!r}")
        if operation in visited:
            return
        visiting.add(operation)
        for dependency in normalized[operation]:
            visit(dependency)
        visiting.remove(operation)
        visited.add(operation)

    for operation in sorted(normalized):
        visit(operation)
    return normalized


def _safe_action_id(action: Mapping[str, Any]) -> str:
    value = action.get("candidate_id") if isinstance(action, Mapping) else None
    if isinstance(value, str) and CANDIDATE_ID_PATTERN.fullmatch(value):
        return value
    return "unknown"


def _validate_action_shape(
    action: Mapping[str, Any],
) -> list[ConstraintViolation]:
    violations: list[ConstraintViolation] = []
    if not isinstance(action, Mapping):
        return [ConstraintViolation.create("INVALID_ACTION", field="action")]

    missing = ACTION_FIELDS.difference(action)
    unknown = set(action).difference(ACTION_FIELDS)
    for field in sorted(missing):
        violations.append(ConstraintViolation.create("INVALID_ACTION", field=field))
    for field in sorted(unknown):
        violations.append(ConstraintViolation.create("INVALID_ACTION", field=f"unknown:{field}"))
    if missing:
        return violations

    if not isinstance(action["candidate_id"], str) or not CANDIDATE_ID_PATTERN.fullmatch(
        action["candidate_id"]
    ):
        violations.append(ConstraintViolation.create("INVALID_ACTION", field="candidate_id"))
    for field in ("operation_class", "tool", "target"):
        if not isinstance(action[field], str) or not action[field].strip():
            violations.append(ConstraintViolation.create("INVALID_ACTION", field=field))
    if not isinstance(action["arguments"], dict):
        violations.append(ConstraintViolation.create("INVALID_ACTION", field="arguments"))
    dependencies = action["declared_dependencies"]
    if (
        not isinstance(dependencies, list)
        or any(not isinstance(item, str) or not item for item in dependencies)
        or len(dependencies) != len(set(dependencies))
    ):
        violations.append(
            ConstraintViolation.create("INVALID_ACTION", field="declared_dependencies")
        )

    objectives = action["estimated_objectives"]
    if not isinstance(objectives, dict) or set(objectives) != OBJECTIVE_FIELDS:
        violations.append(
            ConstraintViolation.create("INVALID_ACTION", field="estimated_objectives")
        )
    else:
        for field in sorted(OBJECTIVE_FIELDS):
            value = objectives[field]
            if (
                isinstance(value, bool)
                or not isinstance(value, int | float)
                or not math.isfinite(value)
                or value < 0
                or (field == "safety_risk" and value > 1)
            ):
                violations.append(
                    ConstraintViolation.create(
                        "INVALID_ACTION",
                        field=f"estimated_objectives.{field}",
                    )
                )
    return violations


def _normalize_virtual_path(value: str) -> str | None:
    if not value or "\\" in value or value.startswith("/"):
        return None
    parts = value.split("/")
    if any(part in {"", ".", ".."} for part in parts):
        return None
    return PurePosixPath(*parts).as_posix()


def _path_is_within(target: str, configured_path: str) -> bool:
    normalized = _normalize_virtual_path(configured_path.rstrip("/"))
    if normalized is None:
        return False
    return target == normalized or target.startswith(normalized + "/")


def _string_sequence(value: object, field: str) -> tuple[str, ...]:
    if not isinstance(value, list) or any(not isinstance(item, str) for item in value):
        raise ValueError(f"{field} must be an array of strings")
    return tuple(value)


def _string_set(value: object, field: str) -> frozenset[str]:
    return frozenset(_string_sequence(value, field))


def _nonnegative_int(value: object, field: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise ValueError(f"{field} must be a non-negative integer")
    return value
