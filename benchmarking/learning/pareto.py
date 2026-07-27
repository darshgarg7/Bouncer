"""Pure multi-objective utilities for offline router evaluation."""

from __future__ import annotations

import math
from collections.abc import Mapping, Sequence
from typing import Any

OBJECTIVES = ("progress", "success", "latency_ms", "cost_units", "adverse_risk")
MAXIMIZE = {"progress", "success"}


def dominates(left: Mapping[str, float], right: Mapping[str, float]) -> bool:
    """Return whether left is no worse everywhere and better somewhere."""
    strict = False
    for objective in OBJECTIVES:
        left_value = _finite(left, objective)
        right_value = _finite(right, objective)
        if objective in MAXIMIZE:
            if left_value < right_value:
                return False
            strict = strict or left_value > right_value
        else:
            if left_value > right_value:
                return False
            strict = strict or left_value < right_value
    return strict


def nondominated(items: Sequence[Mapping[str, Any]]) -> list[Mapping[str, Any]]:
    """Retain the nondominated items without depending on input ordering."""
    result = [
        item
        for index, item in enumerate(items)
        if not any(
            index != other
            and dominates(candidate_objectives(other_item), candidate_objectives(item))
            for other, other_item in enumerate(items)
        )
    ]
    return sorted(result, key=lambda item: str(item.get("candidate_id", "")))


def candidate_objectives(item: Mapping[str, Any]) -> Mapping[str, float]:
    """Read objectives from a flat item or its conservative_objectives member."""
    nested = item.get("conservative_objectives")
    if isinstance(nested, Mapping):
        return nested
    return item


def safety_first(frontier: Sequence[Mapping[str, Any]]) -> Mapping[str, Any]:
    """Apply Bouncer's explicit deterministic selector to a Pareto set."""
    if not frontier:
        raise ValueError("cannot select from an empty frontier")
    return min(
        frontier,
        key=lambda item: (
            _finite(candidate_objectives(item), "adverse_risk"),
            -_finite(candidate_objectives(item), "success"),
            -_finite(candidate_objectives(item), "progress"),
            _finite(candidate_objectives(item), "cost_units"),
            _finite(candidate_objectives(item), "latency_ms"),
            str(item.get("candidate_id", "")),
        ),
    )


def _finite(value: Mapping[str, float], key: str) -> float:
    result = value.get(key)
    if isinstance(result, bool) or not isinstance(result, int | float):
        raise ValueError(f"objective {key} must be numeric")
    number = float(result)
    if not math.isfinite(number):
        raise ValueError(f"objective {key} must be finite")
    return number
