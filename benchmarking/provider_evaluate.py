from __future__ import annotations

import argparse
import hashlib
import json
import os
import subprocess
import sys
import time
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from .baselines import run_baseline
from .evaluate import (
    compact_record,
    compare_conditions,
    run_bouncer,
    summarize,
    validate_analysis_manifest,
)
from .nim_client import ModelConfig

ROOT = Path(__file__).resolve().parents[1]


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Run the resumable real-provider Bouncer benchmark"
    )
    parser.add_argument(
        "--analysis-manifest",
        default="benchmarks/analysis-manifest.json",
    )
    parser.add_argument(
        "--run-manifest",
        default="configs/run-manifest.example.json",
    )
    parser.add_argument("--endpoint", default="")
    parser.add_argument("--output-dir", required=True)
    parser.add_argument("--api-key-env", default="NIM_API_KEY")
    parser.add_argument("--resume", action="store_true")
    parser.add_argument(
        "--max-new-runs",
        type=int,
        default=0,
        help="stop after this many new records; zero runs the complete matrix",
    )
    arguments = parser.parse_args(argv)

    analysis_path = resolve_path(arguments.analysis_manifest)
    run_manifest_path = resolve_path(arguments.run_manifest)
    analysis = load_json(analysis_path)
    run_manifest = load_json(run_manifest_path)
    validate_analysis_manifest(analysis)
    validate_run_manifest(run_manifest)
    task_paths = sorted(ROOT.glob(str(analysis["task_glob"])))
    if len(task_paths) != 10:
        raise SystemExit(f"expected 10 tasks, found {len(task_paths)}")
    endpoint = arguments.endpoint or str(run_manifest["model"]["endpoint"])
    api_key = os.environ.get(arguments.api_key_env, "")
    if not api_key and arguments.api_key_env == "NIM_API_KEY":
        api_key = os.environ.get("NVIDIA_API_KEY", "")
    if endpoint.startswith("https://") and not api_key:
        raise SystemExit(f"{arguments.api_key_env} must be set for an HTTPS provider endpoint")

    output_dir = resolve_path(arguments.output_dir)
    metadata = build_metadata(
        analysis_path,
        run_manifest_path,
        analysis,
        run_manifest,
        task_paths,
        endpoint,
    )
    store = RunStore(output_dir)
    store.initialize(metadata, resume=arguments.resume)

    binary = ROOT / "bin/bouncer-run"
    binary.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(
        ["go", "build", "-o", str(binary), "./cmd/bouncer-run"],
        cwd=ROOT,
        check=True,
    )
    model = ModelConfig(
        endpoint=endpoint,
        model_id=str(run_manifest["model"]["id"]),
        api_key=api_key,
        reasoning_budget=int(run_manifest["model"]["reasoning_budget"]),
        reasoning_budget_parameter=str(
            run_manifest["model"].get("reasoning_budget_parameter", "thinking_token_budget")
        ),
        max_tokens=int(run_manifest["model"]["max_tokens"]),
        temperature=float(run_manifest["model"]["temperature"]),
        top_p=(float(run_manifest["model"]["top_p"]) if "top_p" in run_manifest["model"] else None),
        reasoning_effort=str(run_manifest["model"].get("reasoning_effort", "")),
        timeout_seconds=float(run_manifest["proposal"]["timeout_ms"]) / 1000,
    )
    model.validate()

    expected = [
        (condition, task_path, int(seed))
        for seed in analysis["seeds"]
        for task_path in task_paths
        for condition in analysis["conditions"]
    ]
    started = time.perf_counter()
    new_runs = 0
    for condition, task_path, seed in expected:
        existing = store.load_record(condition, task_path.stem, seed)
        if existing is not None:
            continue
        if arguments.max_new_runs > 0 and new_runs >= arguments.max_new_runs:
            break
        try:
            if condition in {"langgraph", "structured"}:
                record = run_baseline(
                    str(task_path),
                    seed,
                    endpoint,
                    condition,
                    dag_path=str(ROOT / "configs/skill_dag.json"),
                    max_turns=int(run_manifest["benchmark"]["max_turns"]),
                    model=model,
                )
            else:
                record = run_bouncer(
                    binary,
                    task_path,
                    seed,
                    endpoint,
                    manifest_path=run_manifest_path,
                    timeout_seconds=(
                        float(run_manifest["benchmark"]["task_timeout_ms"]) / 1000 + 10
                    ),
                    api_key=api_key,
                )
            store.save_record(condition, task_path.stem, seed, record)
            new_runs += 1
            print(
                json.dumps(
                    {
                        "completed": store.record_count(),
                        "expected": len(expected),
                        "condition": condition,
                        "task": task_path.stem,
                        "seed": seed,
                    }
                ),
                flush=True,
            )
        except Exception as error:
            store.append_failure(condition, task_path.stem, seed, error)
            raise

    full_records = store.load_all_records()
    if len(full_records) != len(expected):
        print(
            json.dumps(
                {
                    "status": "partial",
                    "completed": len(full_records),
                    "expected": len(expected),
                    "output_dir": str(output_dir),
                    "resume_command": build_resume_command(arguments),
                },
                indent=2,
            )
        )
        return 0

    records = [compact_record(record) for record in full_records]
    summaries = {
        condition: summarize([record for record in records if record["condition"] == condition])
        for condition in analysis["conditions"]
    }
    comparisons = compare_conditions(records, summaries, analysis)
    generated_at = datetime.now(UTC).isoformat()
    document = {
        "schema_version": "0.1.0",
        "evaluation_id": f"real-provider-{analysis['evaluation_id']}",
        "evidence_type": "real_provider_with_virtual_execution",
        "generated_at": generated_at,
        "duration_seconds_this_invocation": round(time.perf_counter() - started, 3),
        "configuration_fingerprint": metadata["configuration_fingerprint"],
        "provider": {
            "model_id": run_manifest["model"]["id"],
            "endpoint": endpoint,
            "reasoning_budget_parameter": model.reasoning_budget_parameter,
            "reasoning_effort": model.reasoning_effort,
            "top_p": model.top_p,
        },
        "analysis_manifest": analysis,
        "summaries": summaries,
        "comparisons": comparisons,
        "records": sorted(
            records,
            key=lambda item: (item["condition"], item["task_id"], item["seed"]),
        ),
    }
    store.finalize(document, render_report(document))
    print(
        json.dumps(
            {
                "status": "complete",
                "results": str(store.results_path),
                "report": str(store.report_path),
                "summaries": summaries,
                "comparisons": comparisons,
            },
            indent=2,
        )
    )
    return 0


class RunStore:
    def __init__(self, root: Path) -> None:
        self.root = root
        self.records_dir = root / "records"
        self.metadata_path = root / "run-metadata.json"
        self.failures_path = root / "failures.jsonl"
        self.results_path = root / "results.json"
        self.report_path = root / "report.md"

    def initialize(self, metadata: dict[str, Any], *, resume: bool) -> None:
        if self.root.exists():
            if not resume:
                raise FileExistsError(
                    f"output directory already exists; pass --resume to continue: {self.root}"
                )
            existing = load_json(self.metadata_path)
            if existing.get("configuration_fingerprint") != metadata.get(
                "configuration_fingerprint"
            ):
                raise ValueError("resume configuration does not match run metadata")
            self.records_dir.mkdir(exist_ok=True)
            return
        self.records_dir.mkdir(parents=True)
        write_json_exclusive(self.metadata_path, metadata)

    def record_path(self, condition: str, task_id: str, seed: int) -> Path:
        return self.records_dir / f"{condition}--{task_id}--seed-{seed}.json"

    def load_record(self, condition: str, task_id: str, seed: int) -> dict[str, Any] | None:
        path = self.record_path(condition, task_id, seed)
        if not path.exists():
            return None
        record = load_json(path)
        if (
            record.get("condition") != condition
            or record.get("task_id") != task_id
            or record.get("seed") != seed
        ):
            raise ValueError(f"record identity mismatch: {path}")
        return record

    def save_record(
        self,
        condition: str,
        task_id: str,
        seed: int,
        record: dict[str, Any],
    ) -> None:
        if (
            record.get("condition") != condition
            or record.get("task_id") != task_id
            or record.get("seed") != seed
        ):
            raise ValueError("record identity does not match storage key")
        write_json_exclusive(self.record_path(condition, task_id, seed), record)

    def append_failure(self, condition: str, task_id: str, seed: int, error: Exception) -> None:
        entry = {
            "timestamp": datetime.now(UTC).isoformat(),
            "condition": condition,
            "task_id": task_id,
            "seed": seed,
            "error_type": type(error).__name__,
            "error": str(error),
        }
        with self.failures_path.open("a", encoding="utf-8") as handle:
            handle.write(json.dumps(entry, sort_keys=True) + "\n")

    def load_all_records(self) -> list[dict[str, Any]]:
        return [load_json(path) for path in sorted(self.records_dir.glob("*.json"))]

    def record_count(self) -> int:
        return sum(1 for _ in self.records_dir.glob("*.json"))

    def finalize(self, document: dict[str, Any], report: str) -> None:
        if self.results_path.exists() or self.report_path.exists():
            if not self.results_path.exists() or not self.report_path.exists():
                raise FileExistsError("evaluation has only one final artifact; refusing overwrite")
            existing = load_json(self.results_path)
            if existing.get("configuration_fingerprint") != document.get(
                "configuration_fingerprint"
            ):
                raise ValueError("existing final artifacts have a different configuration")
            return
        write_json_exclusive(self.results_path, document)
        with self.report_path.open("x", encoding="utf-8") as handle:
            handle.write(report)


def build_metadata(
    analysis_path: Path,
    run_manifest_path: Path,
    analysis: dict[str, Any],
    run_manifest: dict[str, Any],
    task_paths: list[Path],
    endpoint: str,
) -> dict[str, Any]:
    frozen = {
        "analysis_manifest_sha256": file_sha256(analysis_path),
        "run_manifest_sha256": file_sha256(run_manifest_path),
        "task_sha256": {path.name: file_sha256(path) for path in task_paths},
        "conditions": analysis["conditions"],
        "seeds": analysis["seeds"],
        "model_id": run_manifest["model"]["id"],
        "endpoint": endpoint,
        "reasoning_budget_parameter": run_manifest["model"].get(
            "reasoning_budget_parameter", "thinking_token_budget"
        ),
    }
    encoded = json.dumps(frozen, separators=(",", ":"), sort_keys=True).encode()
    return {
        "schema_version": "0.1.0",
        "created_at": datetime.now(UTC).isoformat(),
        "configuration_fingerprint": hashlib.sha256(encoded).hexdigest(),
        **frozen,
    }


def validate_run_manifest(manifest: dict[str, Any]) -> None:
    if manifest.get("schema_version") != "0.1.0":
        raise ValueError("run manifest schema_version must be 0.1.0")
    model = manifest.get("model")
    benchmark = manifest.get("benchmark")
    proposal = manifest.get("proposal")
    if (
        not isinstance(model, dict)
        or not isinstance(benchmark, dict)
        or not isinstance(proposal, dict)
    ):
        raise ValueError("run manifest model, proposal, and benchmark are required")
    if benchmark.get("transport") != "in_process":
        raise ValueError("provider evaluation requires in_process transport")


def render_report(document: dict[str, Any]) -> str:
    summaries = document["summaries"]
    comparison = document["comparisons"]
    run_count = sum(summary["attempts"] for summary in summaries.values())
    lines = [
        "# Bouncer Real-Provider Evaluation",
        "",
        f"**Evaluation:** `{document['evaluation_id']}`",
        f"**Generated:** {document['generated_at']}",
        f"**Runs:** {run_count}",
        f"**Model:** `{document['provider']['model_id']}`",
        "",
        (
            "> This run uses provider-reported model telemetry but still executes against "
            "the reversible virtual state. It is not production-sandbox or causal evidence."
        ),
        "",
        "## Results",
        "",
        (
            "| Condition | Pass rate | Severe virtual-mutation runs | "
            "Mean tokens / success | Mean calls | Mean latency |"
        ),
        "| --- | ---: | ---: | ---: | ---: | ---: |",
    ]
    for condition in ("langgraph", "structured", "bouncer"):
        summary = summaries[condition]
        mean_tokens = summary["mean_total_tokens_per_successful_task"]
        token_text = f"{mean_tokens:,.0f}" if mean_tokens is not None else "n/a"
        lines.append(
            f"| {condition} | {summary['pass_rate']:.1%} | "
            f"{summary['runs_with_severe_mutation']}/{summary['attempts']} | "
            f"{token_text} | {summary['mean_model_calls']:.2f} | "
            f"{summary['mean_duration_ms']:.0f} ms |"
        )
    lines.extend(
        [
            "",
            "## Preregistered comparison",
            "",
            f"- Pass-rate delta: {comparison['pass_rate_delta']:+.1%}",
            (
                "- Relative token delta: "
                + format_optional_percent(comparison["relative_token_delta"])
            ),
            (f"- Severe-run rate difference: {comparison['severe_run_rate_difference']:+.1%}"),
            f"- Decision: `{comparison['decision']}`",
            "",
            "## Evidence boundary",
            "",
            "- Model quality, tokenization, latency, and failures are provider-derived.",
            "- State mutations and task oracles remain virtual and fixture-specific.",
            "- The run does not authorize real tools or validate causal estimators.",
            "",
            "Full per-run traces are stored as immutable files under `records/`.",
        ]
    )
    return "\n".join(lines) + "\n"


def write_json_exclusive(path: Path, value: Any) -> None:
    with path.open("x", encoding="utf-8") as handle:
        json.dump(value, handle, indent=2, sort_keys=True)
        handle.write("\n")


def format_optional_percent(value: float | None) -> str:
    return f"{value:+.1%}" if value is not None else "not estimable"


def load_json(path: Path) -> dict[str, Any]:
    with path.open("r", encoding="utf-8") as handle:
        value = json.load(handle)
    if not isinstance(value, dict):
        raise ValueError(f"JSON document must be an object: {path}")
    return value


def file_sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def resolve_path(value: str) -> Path:
    path = Path(value)
    return path if path.is_absolute() else ROOT / path


def build_resume_command(arguments: argparse.Namespace) -> str:
    command = [
        sys.executable,
        "-m",
        "benchmarking.provider_evaluate",
        "--analysis-manifest",
        str(arguments.analysis_manifest),
        "--run-manifest",
        str(arguments.run_manifest),
        "--output-dir",
        str(arguments.output_dir),
        "--resume",
    ]
    if arguments.endpoint:
        command.extend(["--endpoint", arguments.endpoint])
    return " ".join(command)


if __name__ == "__main__":
    raise SystemExit(main())
