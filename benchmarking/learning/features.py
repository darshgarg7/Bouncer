"""Deterministic offline mirror of Bouncer's Go feature extractor."""

from __future__ import annotations

import json
import math
from collections.abc import Mapping
from typing import Any

FEATURE_SCHEMA_VERSION = "0.1.0"
OPERATION_CLASSES = (
    "filesystem.read",
    "filesystem.write",
    "filesystem.delete",
    "state.validate",
    "state.backup",
    "command.run",
    "service.deploy",
    "task.complete",
)
_BASE_FEATURES = (
    "turn_fraction",
    "remaining_turn_fraction",
    "mutation_fraction",
    "remaining_mutation_fraction",
    "completed_operation_count",
    "file_count",
    "constraint_feedback_count",
    "recent_rejection_count",
    "no_progress_streak",
    "candidate_mutating",
    "dependency_count",
    "dependency_satisfaction_ratio",
    "argument_count",
    "argument_bytes_log1p",
    "target_depth",
    "calibrated_latency_log1p",
    "calibrated_cost_log1p",
    "calibrated_safety_risk",
    "mutation_budget_interaction",
    "risk_mutation_interaction",
    "transition_log_probability",
    "transition_unseen",
)
FEATURE_NAMES = _BASE_FEATURES + tuple(
    name
    for operation in OPERATION_CLASSES
    for name in (f"operation={operation}", f"previous={operation}")
)


def extract_features(
    context: Mapping[str, Any],
    candidate: Mapping[str, Any],
    transition_prior: Mapping[str, Any],
) -> dict[str, float]:
    """Return the exact feature vector expected by the Go runtime."""
    features = dict.fromkeys(FEATURE_NAMES, 0.0)
    max_turns = max(_integer(context, "max_turns"), 1)
    turn = min(max(_integer(context, "turn"), 0), max_turns)
    state = _mapping(context, "state")
    policy = _mapping(context, "policy")
    max_mutations = max(_integer(policy, "max_mutations"), 1)
    mutations = min(max(_integer(state, "mutation_count"), 0), max_mutations)
    completed = _string_list(state, "completed_operations")
    files = _mapping(state, "files")
    feedback = state.get("constraint_feedback", [])
    if not isinstance(feedback, list):
        raise ValueError("state.constraint_feedback must be an array")

    features["turn_fraction"] = turn / max_turns
    features["remaining_turn_fraction"] = max(max_turns - turn, 0) / max_turns
    features["mutation_fraction"] = mutations / max_mutations
    features["remaining_mutation_fraction"] = max(max_mutations - mutations, 0) / max_mutations
    features["completed_operation_count"] = _bounded_count(len(completed))
    features["file_count"] = _bounded_count(len(files))
    features["constraint_feedback_count"] = _bounded_count(len(feedback))
    features["recent_rejection_count"] = _bounded_count(_integer(context, "recent_rejections"))
    features["no_progress_streak"] = _bounded_count(_integer(context, "no_progress_streak"))

    operation = _string(candidate, "operation_class")
    arguments = _mapping(candidate, "arguments")
    dependencies = _string_list(candidate, "declared_dependencies")
    target = _string(candidate, "target")
    objectives = _mapping(candidate, "routing_objectives")
    mutating = operation in {"filesystem.write", "filesystem.delete", "service.deploy"}
    features["candidate_mutating"] = float(mutating)
    features["dependency_count"] = _bounded_count(len(dependencies))
    features["dependency_satisfaction_ratio"] = (
        sum(dependency in set(completed) for dependency in dependencies) / len(dependencies)
        if dependencies
        else 1.0
    )
    features["argument_count"] = _bounded_count(len(arguments))
    encoded_arguments = json.dumps(
        arguments,
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=False,
    ).encode()
    features["argument_bytes_log1p"] = math.log1p(min(len(encoded_arguments), 1 << 20))
    features["target_depth"] = _bounded_count(target.count("/") + 1)
    features["calibrated_latency_log1p"] = math.log1p(
        _non_negative_number(objectives, "latency_ms")
    )
    features["calibrated_cost_log1p"] = math.log1p(_non_negative_number(objectives, "cost_units"))
    features["calibrated_safety_risk"] = _probability(objectives, "safety_risk")
    features["mutation_budget_interaction"] = (
        features["candidate_mutating"] * features["remaining_mutation_fraction"]
    )
    features["risk_mutation_interaction"] = (
        features["candidate_mutating"] * features["calibrated_safety_risk"]
    )

    previous = context.get("previous_operation") or "START"
    if not isinstance(previous, str):
        raise ValueError("previous_operation must be a string")
    probability, seen = transition_probability(transition_prior, previous, operation)
    features["transition_log_probability"] = max(math.log(probability), -20.0)
    features["transition_unseen"] = float(not seen)
    if operation in OPERATION_CLASSES:
        features[f"operation={operation}"] = 1.0
    if previous in OPERATION_CLASSES:
        features[f"previous={previous}"] = 1.0
    return features


def transition_probability(
    prior: Mapping[str, Any], previous: str, operation: str
) -> tuple[float, bool]:
    """Return a smoothed transition probability and whether it was observed."""
    fallback = _probability(prior, "fallback_probability", exclusive_zero=True)
    probabilities = _mapping(prior, "probabilities")
    next_operations = probabilities.get(previous)
    if isinstance(next_operations, Mapping) and operation in next_operations:
        value = next_operations[operation]
        if isinstance(value, bool) or not isinstance(value, int | float):
            raise ValueError("transition probability must be numeric")
        probability = float(value)
        if not 0 < probability <= 1 or not math.isfinite(probability):
            raise ValueError("transition probability must be in (0,1]")
        return probability, True
    return fallback, False


def _mapping(value: Mapping[str, Any], key: str) -> Mapping[str, Any]:
    result = value.get(key)
    if not isinstance(result, Mapping):
        raise ValueError(f"{key} must be an object")
    return result


def _string(value: Mapping[str, Any], key: str) -> str:
    result = value.get(key)
    if not isinstance(result, str) or not result:
        raise ValueError(f"{key} must be a non-empty string")
    return result


def _string_list(value: Mapping[str, Any], key: str) -> list[str]:
    result = value.get(key)
    if not isinstance(result, list) or any(not isinstance(item, str) for item in result):
        raise ValueError(f"{key} must be an array of strings")
    return result


def _integer(value: Mapping[str, Any], key: str) -> int:
    result = value.get(key, 0)
    if isinstance(result, bool) or not isinstance(result, int):
        raise ValueError(f"{key} must be an integer")
    return result


def _non_negative_number(value: Mapping[str, Any], key: str) -> float:
    result = value.get(key)
    if isinstance(result, bool) or not isinstance(result, int | float):
        raise ValueError(f"{key} must be numeric")
    number = float(result)
    if not math.isfinite(number) or number < 0:
        raise ValueError(f"{key} must be finite and non-negative")
    return number


def _probability(value: Mapping[str, Any], key: str, *, exclusive_zero: bool = False) -> float:
    number = _non_negative_number(value, key)
    if number > 1 or (exclusive_zero and number == 0):
        interval = "(0,1]" if exclusive_zero else "[0,1]"
        raise ValueError(f"{key} must be in {interval}")
    return number


def _bounded_count(value: int) -> float:
    return math.log1p(max(value, 0))
