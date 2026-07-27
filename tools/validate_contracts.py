#!/usr/bin/env python3
"""Validate Bouncer's frozen Phase 0 JSON documents."""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from jsonschema import Draft202012Validator

ROOT = Path(__file__).resolve().parents[1]


def load(path: Path) -> object:
    """Read a JSON document from disk."""
    with path.open("r", encoding="utf-8") as handle:
        return json.load(handle)


def validate(schema_path: Path, document_path: Path) -> None:
    """Validate one document against a Draft 2020-12 JSON schema."""
    schema = load(schema_path)
    Draft202012Validator.check_schema(schema)
    document = load(document_path)
    validator = Draft202012Validator(
        schema,
        format_checker=Draft202012Validator.FORMAT_CHECKER,
    )
    errors = sorted(validator.iter_errors(document), key=lambda error: list(error.path))
    if errors:
        rendered = "\n".join(
            f"{document_path}:{'/'.join(map(str, error.path))}: {error.message}" for error in errors
        )
        raise SystemExit(rendered)


def main() -> None:
    """Validate every checked-in schema, manifest, task, and scenario."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--objective-calibration",
        type=Path,
        action="append",
        default=[],
        help="additional objective-calibration artifact to validate",
    )
    parser.add_argument(
        "--learning-artifact",
        type=Path,
        action="append",
        default=[],
        help="additional portable learning artifact to validate",
    )
    parser.add_argument(
        "--anomaly-artifact",
        type=Path,
        action="append",
        default=[],
        help="additional portable Isolation Forest artifact to validate",
    )
    args = parser.parse_args()
    schema_paths = sorted((ROOT / "schemas").glob("*.schema.json"))
    for schema_path in schema_paths:
        Draft202012Validator.check_schema(load(schema_path))

    event_schema = load(ROOT / "schemas/event.schema.json")
    expected_event_types = {
        "run.started",
        "run.completed",
        "run.failed",
        "proposal.requested",
        "proposal.completed",
        "proposal.expanded",
        "proposal.failed",
        "constraint.evaluated",
        "candidate.selected",
        "execution.completed",
        "task.completed",
    }
    if not isinstance(event_schema, dict):
        raise SystemExit("event schema is malformed")
    properties = event_schema.get("properties")
    if not isinstance(properties, dict):
        raise SystemExit("event schema properties are malformed")
    event_type = properties.get("event_type")
    if not isinstance(event_type, dict) or set(event_type.get("enum", [])) != expected_event_types:
        raise SystemExit("event schema does not contain the frozen event type set")

    validate(
        ROOT / "schemas/run-manifest.schema.json",
        ROOT / "configs/run-manifest.example.json",
    )
    validate(
        ROOT / "schemas/run-manifest.schema.json",
        ROOT / "configs/run-manifest.synthetic-v1.json",
    )
    validate(
        ROOT / "schemas/run-manifest.schema.json",
        ROOT / "configs/run-manifest.nvidia-hosted.json",
    )
    calibration_paths = sorted((ROOT / "configs").glob("objective-calibration.*.json"))
    if not calibration_paths:
        raise SystemExit("expected at least one checked-in objective-calibration artifact")
    for calibration_path in calibration_paths:
        validate(ROOT / "schemas/objective-calibration.schema.json", calibration_path)
    for calibration_path in args.objective_calibration:
        validate(ROOT / "schemas/objective-calibration.schema.json", calibration_path)
    validate(
        ROOT / "schemas/learning-artifact.schema.json",
        ROOT / "configs/learning-artifact.bootstrap.json",
    )
    for learning_path in args.learning_artifact:
        validate(ROOT / "schemas/learning-artifact.schema.json", learning_path)
    validate(
        ROOT / "schemas/anomaly-artifact.schema.json",
        ROOT / "configs/anomaly-artifact.bootstrap.json",
    )
    for anomaly_path in args.anomaly_artifact:
        validate(ROOT / "schemas/anomaly-artifact.schema.json", anomaly_path)
    task_schema = ROOT / "schemas/task.schema.json"
    task_paths = sorted((ROOT / "benchmarks/tasks").glob("task-*.json"))
    if len(task_paths) != 10:
        raise SystemExit(f"expected 10 smoke tasks, found {len(task_paths)}")
    for task_path in task_paths:
        validate(task_schema, task_path)
    validate(
        ROOT / "schemas/scenarios.schema.json",
        ROOT / "benchmarks/scenarios.json",
    )
    validate(
        ROOT / "schemas/analysis-manifest.schema.json",
        ROOT / "benchmarks/analysis-manifest.json",
    )
    validate(
        ROOT / "schemas/ablation-manifest.schema.json",
        ROOT / "benchmarks/ablation-manifest.json",
    )
    validate(
        ROOT / "schemas/projector-ablation-manifest.schema.json",
        ROOT / "benchmarks/projector-ablation-manifest.json",
    )
    validate(
        ROOT / "schemas/mechanism-manifest.schema.json",
        ROOT / "benchmarks/mechanism-manifest.json",
    )
    validate(
        ROOT / "tools/publication-claims.schema.json",
        ROOT / "benchmarks/publication-claims.json",
    )
    task_ids = {path.stem for path in task_paths}
    scenarios = load(ROOT / "benchmarks/scenarios.json")
    if not isinstance(scenarios, dict) or not isinstance(scenarios.get("scenarios"), dict):
        raise SystemExit("scenario document is malformed")
    if set(scenarios["scenarios"]) != task_ids:
        raise SystemExit("scenario task IDs do not match smoke-task IDs")
    print(
        f"validated {len(schema_paths)} schemas, the run, analysis, and ablation manifests, "
        f"{len(task_paths)} smoke tasks, and matching simulator scenarios"
    )


if __name__ == "__main__":
    main()
