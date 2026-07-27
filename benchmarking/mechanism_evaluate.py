from __future__ import annotations

import argparse
import json
import statistics
import subprocess
import time
from datetime import UTC, datetime
from typing import Any

from .evaluate import ROOT, compact_record, run_bouncer, summarize
from .mock_nim import start_mock_nim


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Run the policy-held-constant mechanism study")
    parser.add_argument("--manifest", default="benchmarks/mechanism-manifest.json")
    parser.add_argument("--results", default="benchmarks/reports/mechanism-results.json")
    parser.add_argument("--report", default="benchmarks/reports/mechanism.md")
    arguments = parser.parse_args(argv)
    manifest = json.loads((ROOT / arguments.manifest).read_text(encoding="utf-8"))
    validate_manifest(manifest)
    tasks = sorted(ROOT.glob(manifest["task_glob"]))
    if len(tasks) != 10:
        raise ValueError(f"expected 10 smoke tasks, found {len(tasks)}")
    binary = ROOT / "bin/bouncer-run"
    binary.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(["go", "build", "-o", str(binary), "./cmd/bouncer-run"], cwd=ROOT, check=True)

    records: list[dict[str, Any]] = []
    started = time.perf_counter()
    with start_mock_nim(ROOT / "benchmarks/scenarios.json") as server:
        for condition in manifest["conditions"]:
            for seed in manifest["seeds"]:
                for task in tasks:
                    result = run_bouncer(
                        binary,
                        task,
                        seed,
                        server.endpoint,
                        extra_args=condition["arguments"],
                    )
                    result["condition"] = condition["name"]
                    records.append(compact_record(result))
    summaries = {
        condition["name"]: summarize(
            [record for record in records if record["condition"] == condition["name"]]
        )
        for condition in manifest["conditions"]
    }
    comparisons = compare_to_reference(records, summaries, manifest["reference_condition"])
    document = {
        "schema_version": "0.1.0",
        "evaluation_id": manifest["evaluation_id"],
        "generated_at": datetime.now(UTC).isoformat(),
        "duration_seconds": round(time.perf_counter() - started, 3),
        "interpretation": manifest["interpretation"],
        "manifest": manifest,
        "summaries": summaries,
        "comparisons": comparisons,
        "records": sorted(
            records, key=lambda record: (record["condition"], record["task_id"], record["seed"])
        ),
    }
    results_path = ROOT / arguments.results
    report_path = ROOT / arguments.report
    results_path.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    report_path.write_text(render_report(document), encoding="utf-8")
    print(json.dumps({"results": str(results_path), "report": str(report_path)}, indent=2))
    return 0


def validate_manifest(manifest: object) -> None:
    if not isinstance(manifest, dict) or manifest.get("schema_version") != "0.1.0":
        raise ValueError("mechanism manifest schema_version must be 0.1.0")
    if manifest.get("reference_condition") != "single_policy":
        raise ValueError("mechanism reference condition must be single_policy")
    conditions = manifest.get("conditions")
    if not isinstance(conditions, list) or len(conditions) < 2:
        raise ValueError("mechanism manifest requires at least two conditions")
    names = [condition.get("name") for condition in conditions if isinstance(condition, dict)]
    if (
        len(names) != len(conditions)
        or len(names) != len(set(names))
        or "single_policy" not in names
    ):
        raise ValueError("mechanism condition names must be unique and include single_policy")


def compare_to_reference(
    records: list[dict[str, Any]], summaries: dict[str, dict[str, Any]], reference: str
) -> dict[str, dict[str, float | None]]:
    reference_records = {
        (record["task_id"], record["seed"]): record
        for record in records
        if record["condition"] == reference
    }
    comparisons: dict[str, dict[str, float | None]] = {}
    for condition, summary in summaries.items():
        paired = [
            (reference_records[(record["task_id"], record["seed"])], record)
            for record in records
            if record["condition"] == condition
        ]
        token_differences = [right["total_tokens"] - left["total_tokens"] for left, right in paired]
        comparisons[condition] = {
            "pass_rate_delta": round(summary["pass_rate"] - summaries[reference]["pass_rate"], 4),
            "mean_paired_token_difference": round(statistics.fmean(token_differences), 2),
            "severe_run_rate_delta": round(
                summary["runs_with_severe_mutation_rate"]
                - summaries[reference]["runs_with_severe_mutation_rate"],
                4,
            ),
        }
    return comparisons


def render_report(document: dict[str, Any]) -> str:
    lines = [
        "# Bouncer controlled mechanism study",
        "",
        f"**Evaluation:** `{document['evaluation_id']}`",
        f"**Generated:** {document['generated_at']}",
        f"**Wall time:** {document['duration_seconds']} seconds",
        "",
        (
            "> This is deterministic smoke-suite evidence with synthetic provider telemetry. "
            "It measures implementation behavior, not real-model capability, production safety, "
            "or causal effects."
        ),
        "",
        (
            "| Condition | Pass rate | Severe runs | Mean tokens/success | Mean calls | "
            "Mean candidates | Token delta vs single+policy |"
        ),
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for condition in document["manifest"]["conditions"]:
        name = condition["name"]
        summary = document["summaries"][name]
        comparison = document["comparisons"][name]
        tokens = summary["mean_total_tokens_per_successful_task"]
        lines.append(
            f"| {name} | {summary['pass_rate']:.1%} | "
            f"{summary['runs_with_severe_mutation']}/{summary['attempts']} | "
            f"{tokens:,.0f} | {summary['mean_model_calls']:.2f} | "
            f"{summary['mean_generated_candidates']:.2f} | "
            f"{comparison['mean_paired_token_difference']:+,.0f} |"
        )
    lines.extend(
        [
            "",
            (
                "The deterministic policy is identical across conditions. This isolates proposal "
                "width, routing semantics, exploration, and adaptive expansion from the unsafe "
                "unfiltered baseline in the historical integration study."
            ),
            "",
            "Raw paired records are in [`mechanism-results.json`](mechanism-results.json).",
            "",
        ]
    )
    return "\n".join(lines)


if __name__ == "__main__":
    raise SystemExit(main())
