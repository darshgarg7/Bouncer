"""Build joined learning observations from Bouncer's tamper-evident event logs.

Callers must run the standalone Go verifier before training. This builder checks
join, sequence, and identity semantics but deliberately does not duplicate the
canonical hash implementation.
"""

from __future__ import annotations

import argparse
import json
import math
from collections.abc import Iterable, Mapping
from pathlib import Path
from typing import Any

SCHEMA_VERSION = "0.1.0"


def read_events(path: Path) -> list[dict[str, Any]]:
    """Read one event log while enforcing sequence and identity continuity."""
    events: list[dict[str, Any]] = []
    run_id: str | None = None
    task_id: str | None = None
    with path.open(encoding="utf-8") as handle:
        for line_number, line in enumerate(handle, start=1):
            if not line.strip():
                continue
            value = json.loads(line)
            if not isinstance(value, dict):
                raise ValueError(f"{path}:{line_number}: event must be an object")
            if value.get("sequence") != len(events) + 1:
                raise ValueError(f"{path}:{line_number}: event sequence is not contiguous")
            current_run = _required_string(value, "run_id")
            current_task = _required_string(value, "task_id")
            run_id = run_id or current_run
            task_id = task_id or current_task
            if current_run != run_id or current_task != task_id:
                raise ValueError(f"{path}:{line_number}: run or task identity changed")
            events.append(value)
    if not events or events[0].get("event_type") != "run.started":
        raise ValueError(f"{path}: event log must begin with run.started")
    if events[-1].get("event_type") not in {"run.completed", "run.failed"}:
        raise ValueError(f"{path}: event log is missing a terminal run event")
    return events


def build_trajectory(events: list[dict[str, Any]]) -> dict[str, Any]:
    """Join routing decisions, measured outcomes, and the terminal task label."""
    start = events[0]
    run_id = _required_string(start, "run_id")
    task_id = _required_string(start, "task_id")
    start_payload = _mapping(start, "payload")
    policy_sha256 = _required_string(start_payload, "policy_sha256")
    calibration = _mapping(start_payload, "objective_calibration")
    calibration_sha256 = _required_string(calibration, "artifact_sha256")
    decisions: dict[int, dict[str, Any]] = {}
    outcomes: dict[int, dict[str, Any]] = {}
    for event in events[1:-1]:
        step = _required_integer(event, "step_id")
        payload = _mapping(event, "payload")
        if event.get("event_type") == "candidate.selected":
            decisions[step] = _routing_decision(
                run_id,
                task_id,
                step,
                payload,
                policy_sha256,
                calibration_sha256,
            )
        elif event.get("event_type") == "execution.completed":
            outcomes[step] = _measured_outcome(run_id, task_id, step, payload)
    if set(decisions) != set(outcomes):
        missing_outcomes = sorted(set(decisions) - set(outcomes))
        missing_decisions = sorted(set(outcomes) - set(decisions))
        raise ValueError(
            f"{run_id}: broken trajectory; missing outcomes={missing_outcomes}, "
            f"missing decisions={missing_decisions}"
        )
    terminal = events[-1]
    terminal_payload = _mapping(terminal, "payload")
    passed = bool(terminal_payload.get("passed", False))
    completed = bool(terminal_payload.get("task_complete", False))
    censored = terminal.get("event_type") != "run.completed"
    ordered_steps = sorted(decisions)
    transitions: list[dict[str, Any]] = []
    future_progress = 0.0
    future_latency = 0.0
    future_cost = 0.0
    future_adverse = 0.0
    reverse: list[dict[str, Any]] = []
    for step in reversed(ordered_steps):
        outcome = outcomes[step]
        future_progress += float(outcome["progress_delta"])
        future_latency += float(outcome["latency_ms"])
        if not outcome["cost_censored"]:
            future_cost += float(outcome["cost_units"])
        future_adverse += float(bool(outcome["adverse"]))
        reverse.append(
            {
                "decision": decisions[step],
                "outcome": outcome,
                "reward_to_go": {
                    "progress": future_progress,
                    "success": float(passed),
                    "latency_ms": future_latency,
                    "cost_units": future_cost,
                    "adverse_risk": future_adverse,
                },
            }
        )
    transitions.extend(reversed(reverse))
    if not transitions:
        raise ValueError(f"{run_id}: trajectory contains no executed transitions")
    return {
        "schema_version": SCHEMA_VERSION,
        "run_id": run_id,
        "task_id": task_id,
        "passed": passed,
        "terminal": completed,
        "censored": censored,
        "transitions": transitions,
    }


def build_trajectories(paths: Iterable[Path]) -> list[dict[str, Any]]:
    """Build trajectories in stable run-ID order."""
    trajectories = [build_trajectory(read_events(path)) for path in paths]
    run_ids = [trajectory["run_id"] for trajectory in trajectories]
    if len(run_ids) != len(set(run_ids)):
        raise ValueError("input contains duplicate run IDs")
    return sorted(trajectories, key=lambda trajectory: str(trajectory["run_id"]))


def write_jsonl(path: Path, values: Iterable[Mapping[str, Any]]) -> None:
    """Write evidence without replacing an existing artifact."""
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("x", encoding="utf-8") as handle:
        for value in values:
            handle.write(json.dumps(value, sort_keys=True, allow_nan=False) + "\n")


def _routing_decision(
    run_id: str,
    task_id: str,
    step: int,
    payload: Mapping[str, Any],
    policy_sha256: str,
    calibration_sha256: str,
) -> dict[str, Any]:
    decision_id = _required_string(payload, "decision_id")
    state = dict(_mapping(payload, "state"))
    raw_candidates = payload.get("eligible_candidates")
    if not isinstance(raw_candidates, list) or not raw_candidates:
        raise ValueError(f"{decision_id}: eligible_candidates must be a non-empty array")
    learning_payload = payload.get("learning")
    features_by_id: dict[str, dict[str, float]] = {}
    predictions: list[dict[str, Any]] = []
    learning_record: dict[str, Any] | None = None
    artifact_sha256: str | None = None
    if isinstance(learning_payload, Mapping) and learning_payload.get("mode") in {
        "shadow",
        "active",
    }:
        metadata = _mapping(learning_payload, "metadata")
        artifact_sha256 = _required_string(metadata, "artifact_sha256")
        raw_predictions = learning_payload.get("predictions")
        if not isinstance(raw_predictions, list) or not raw_predictions:
            raise ValueError(f"{decision_id}: learned predictions are missing")
        for value in raw_predictions:
            if not isinstance(value, Mapping):
                raise ValueError(f"{decision_id}: learned prediction must be an object")
            candidate_id = _required_string(value, "candidate_id")
            raw_features = _mapping(value, "features")
            features_by_id[candidate_id] = {
                name: _finite_number(feature, f"feature {name}")
                for name, feature in raw_features.items()
            }
            predictions.append(
                {
                    "candidate_id": candidate_id,
                    "progress": dict(_mapping(value, "progress")),
                    "success": dict(_mapping(value, "success")),
                    "latency_ms": dict(_mapping(value, "latency_ms")),
                    "cost_units": dict(_mapping(value, "cost_units")),
                    "adverse_risk": dict(_mapping(value, "adverse_risk")),
                }
            )
        frontier = learning_payload.get("frontier_candidate_ids")
        if not isinstance(frontier, list) or any(not isinstance(item, str) for item in frontier):
            raise ValueError(f"{decision_id}: learned frontier is malformed")
        learning_record = {
            "mode": learning_payload["mode"],
            "artifact_id": _required_string(metadata, "artifact_id"),
            "artifact_sha256": artifact_sha256,
            "frontier_candidate_ids": frontier,
            "predictions": predictions,
        }
    candidates: list[dict[str, Any]] = []
    for raw in raw_candidates:
        if not isinstance(raw, Mapping):
            raise ValueError(f"{decision_id}: candidate evidence must be an object")
        candidate_id = _required_string(raw, "candidate_id")
        candidate = dict(raw)
        if candidate_id in features_by_id:
            candidate["features"] = features_by_id[candidate_id]
        candidates.append(candidate)
    probability = _finite_number(payload.get("behavior_probability"), "behavior_probability")
    if not 0 < probability <= 1:
        raise ValueError(f"{decision_id}: behavior_probability must be in (0,1]")
    return {
        "schema_version": SCHEMA_VERSION,
        "run_id": run_id,
        "task_id": task_id,
        "decision_id": decision_id,
        "turn": step,
        "state": state,
        "candidates": candidates,
        "selected_candidate_id": _required_string(payload, "action_id"),
        "behavior_probability": probability,
        "versions": {
            "feature_schema": str(payload.get("feature_schema_version", "")),
            "policy_sha256": policy_sha256,
            "calibration_sha256": calibration_sha256,
            "learning_artifact_sha256": artifact_sha256,
        },
        "learning": learning_record,
    }


def _measured_outcome(
    run_id: str,
    task_id: str,
    step: int,
    payload: Mapping[str, Any],
) -> dict[str, Any]:
    decision_id = _required_string(payload, "decision_id")
    cost_censored = payload.get("cost_censored")
    if not isinstance(cost_censored, bool):
        raise ValueError(f"{decision_id}: cost_censored must be boolean")
    cost_value = payload.get("cost_units")
    if cost_censored:
        if cost_value is not None:
            raise ValueError(f"{decision_id}: censored cost must be null")
    else:
        cost_value = _non_negative_number(cost_value, "cost_units")
    return {
        "schema_version": SCHEMA_VERSION,
        "run_id": run_id,
        "task_id": task_id,
        "decision_id": decision_id,
        "turn": step,
        "candidate_id": _required_string(payload, "action_id"),
        "state_before_sha256": _required_sha256(payload, "state_before_sha256"),
        "state_after_sha256": _required_sha256(payload, "state_after_sha256"),
        "latency_ms": _non_negative_number(payload.get("latency_ms"), "latency_ms"),
        "cost_units": cost_value,
        "cost_censored": cost_censored,
        "progress_before": _probability(payload.get("progress_before"), "progress_before"),
        "progress_after": _probability(payload.get("progress_after"), "progress_after"),
        "progress_delta": _bounded_number(payload.get("progress_delta"), "progress_delta", -1, 1),
        "adverse": _required_boolean(payload, "adverse"),
        "terminal": _required_boolean(payload, "terminal"),
        "censored": _required_boolean(payload, "censored"),
        "censor_reason": payload.get("censor_reason"),
    }


def _mapping(value: Mapping[str, Any], key: str) -> Mapping[str, Any]:
    result = value.get(key)
    if not isinstance(result, Mapping):
        raise ValueError(f"{key} must be an object")
    return result


def _required_string(value: Mapping[str, Any], key: str) -> str:
    result = value.get(key)
    if not isinstance(result, str) or not result:
        raise ValueError(f"{key} must be a non-empty string")
    return result


def _required_sha256(value: Mapping[str, Any], key: str) -> str:
    result = _required_string(value, key)
    if len(result) != 64 or any(character not in "0123456789abcdef" for character in result):
        raise ValueError(f"{key} must be lowercase SHA-256 hex")
    return result


def _required_integer(value: Mapping[str, Any], key: str) -> int:
    result = value.get(key)
    if isinstance(result, bool) or not isinstance(result, int) or result < 0:
        raise ValueError(f"{key} must be a non-negative integer")
    return result


def _required_boolean(value: Mapping[str, Any], key: str) -> bool:
    result = value.get(key)
    if not isinstance(result, bool):
        raise ValueError(f"{key} must be boolean")
    return result


def _finite_number(value: object, label: str) -> float:
    if isinstance(value, bool) or not isinstance(value, int | float):
        raise ValueError(f"{label} must be numeric")
    number = float(value)
    if not math.isfinite(number):
        raise ValueError(f"{label} must be finite")
    return number


def _non_negative_number(value: object, label: str) -> float:
    number = _finite_number(value, label)
    if number < 0:
        raise ValueError(f"{label} must be non-negative")
    return number


def _bounded_number(value: object, label: str, minimum: float, maximum: float) -> float:
    number = _finite_number(value, label)
    if not minimum <= number <= maximum:
        raise ValueError(f"{label} must be in [{minimum},{maximum}]")
    return number


def _probability(value: object, label: str) -> float:
    return _bounded_number(value, label, 0, 1)


def main() -> None:
    """Build a trajectory JSONL artifact from one or more event logs."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--event-log", type=Path, action="append", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    write_jsonl(args.output, build_trajectories(args.event_log))


if __name__ == "__main__":
    main()
