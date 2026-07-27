"""Compare proposal count and beam width on the synthetic task suite."""

from __future__ import annotations

import argparse
import json
import subprocess
import time
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from .evaluate import compact_record, summarize
from .mock_nim import start_mock_nim

ROOT = Path(__file__).resolve().parents[1]


def main(argv: list[str] | None = None) -> int:
    """Run every frozen ablation configuration and write paired results."""
    parser = argparse.ArgumentParser(
        description="Run Bouncer proposal-count and beam-width ablations"
    )
    parser.add_argument(
        "--manifest",
        default="benchmarks/ablation-manifest.json",
    )
    parser.add_argument(
        "--results",
        default="benchmarks/reports/synthetic-ablation-results.json",
    )
    parser.add_argument(
        "--report",
        default="benchmarks/reports/synthetic-ablation.md",
    )
    arguments = parser.parse_args(argv)
    manifest = load_json(ROOT / arguments.manifest)
    validate_manifest(manifest)
    task_paths = sorted(ROOT.glob(str(manifest["task_glob"])))
    if len(task_paths) != 10:
        raise SystemExit(f"expected 10 tasks, found {len(task_paths)}")

    binary = ROOT / "bin/bouncer-run"
    binary.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(
        ["go", "build", "-o", str(binary), "./cmd/bouncer-run"],
        cwd=ROOT,
        check=True,
    )
    records: list[dict[str, Any]] = []
    started = time.perf_counter()
    with start_mock_nim(ROOT / "benchmarks/scenarios.json") as server:
        for configuration in manifest["configurations"]:
            for seed in manifest["seeds"]:
                for task_path in task_paths:
                    record = compact_record(
                        run_variant(binary, task_path, seed, server.endpoint, configuration)
                    )
                    record["configuration"] = configuration["id"]
                    record["proposers"] = configuration["proposers"]
                    record["beam_width"] = configuration["beam_width"]
                    records.append(record)

    summaries = {
        configuration["id"]: summarize(
            [record for record in records if record["configuration"] == configuration["id"]]
        )
        for configuration in manifest["configurations"]
    }
    reference = summaries[manifest["reference_configuration"]]
    for summary in summaries.values():
        reference_tokens = reference["mean_total_tokens_per_successful_task"]
        current_tokens = summary["mean_total_tokens_per_successful_task"]
        summary["relative_token_delta_vs_reference"] = round(
            current_tokens / reference_tokens - 1, 4
        )
    selected = select_configuration(summaries, manifest)
    document = {
        "schema_version": "0.1.0",
        "evaluation_id": manifest["evaluation_id"],
        "generated_at": datetime.now(UTC).isoformat(),
        "duration_seconds": round(time.perf_counter() - started, 3),
        "manifest": manifest,
        "summaries": summaries,
        "selected_configuration": selected,
        "records": sorted(
            records,
            key=lambda item: (item["configuration"], item["task_id"], item["seed"]),
        ),
    }
    results_path = ROOT / arguments.results
    report_path = ROOT / arguments.report
    results_path.parent.mkdir(parents=True, exist_ok=True)
    results_path.write_text(
        json.dumps(document, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    report_path.write_text(render_report(document), encoding="utf-8")
    print(
        json.dumps(
            {
                "results": str(results_path),
                "report": str(report_path),
                "selected_configuration": selected,
                "summaries": summaries,
            },
            indent=2,
        )
    )
    return 0


def run_variant(
    binary: Path,
    task_path: Path,
    seed: int,
    endpoint: str,
    configuration: dict[str, Any],
) -> dict[str, Any]:
    """Run one Bouncer configuration for a single task and seed."""
    process = subprocess.run(
        [
            str(binary),
            "-manifest",
            "configs/run-manifest.example.json",
            "-task",
            str(task_path),
            "-endpoint",
            endpoint,
            "-project-root",
            str(ROOT),
            "-seed",
            str(seed),
            "-proposers",
            str(configuration["proposers"]),
            "-beam-width",
            str(configuration["beam_width"]),
            "-projector-mode",
            str(configuration.get("projector_mode", "subprocess")),
            "-routing-strategy",
            "legacy_crowding",
        ],
        cwd=ROOT,
        text=True,
        capture_output=True,
        check=False,
        timeout=60,
    )
    if process.returncode != 0:
        raise RuntimeError(
            f"variant {configuration['id']} failed for {task_path.name} seed {seed}: "
            f"{process.stderr.strip()}"
        )
    result = json.loads(process.stdout)
    if not isinstance(result, dict):
        raise ValueError("Bouncer ablation result must be an object")
    return result


def select_configuration(summaries: dict[str, dict[str, Any]], manifest: dict[str, Any]) -> str:
    """Choose the cheapest configuration that satisfies the frozen guardrails."""
    reference = summaries[manifest["reference_configuration"]]
    minimum_pass_rate = reference["pass_rate"] + manifest["pass_rate_noninferiority_margin"]
    maximum_severe_rate = reference["runs_with_severe_mutation_rate"]
    eligible = [
        (identifier, summary)
        for identifier, summary in summaries.items()
        if summary["pass_rate"] >= minimum_pass_rate
        and summary["runs_with_severe_mutation_rate"] <= maximum_severe_rate
        and summary["mean_total_tokens_per_successful_task"] is not None
    ]
    if not eligible:
        return str(manifest["reference_configuration"])
    return str(
        min(
            eligible,
            key=lambda item: (
                item[1]["mean_total_tokens_per_successful_task"],
                item[1]["mean_duration_ms"],
                item[0],
            ),
        )[0]
    )


def validate_manifest(manifest: dict[str, Any]) -> None:
    """Validate the fields needed by the proposal ablation runner."""
    if manifest.get("schema_version") != "0.1.0":
        raise ValueError("ablation manifest schema_version must be 0.1.0")
    configurations = manifest.get("configurations")
    if not isinstance(configurations, list) or len(configurations) < 2:
        raise ValueError("ablation requires at least two configurations")
    identifiers = [configuration.get("id") for configuration in configurations]
    if len(set(identifiers)) != len(identifiers):
        raise ValueError("ablation configuration IDs must be unique")
    if manifest.get("reference_configuration") not in identifiers:
        raise ValueError("ablation reference configuration is absent")


def render_report(document: dict[str, Any]) -> str:
    """Render the ablation result as a compact Markdown report."""
    lines = [
        "# Bouncer Synthetic Proposal Ablation",
        "",
        f"**Evaluation:** `{document['evaluation_id']}`",
        f"**Generated:** {document['generated_at']}",
        f"**Runs:** {len(document['records'])}",
        f"**Selected configuration:** `{document['selected_configuration']}`",
        "",
        (
            "> This is a deterministic simulator ablation. It measures architectural "
            "cost and fixture safety, not real-model diversity or production safety."
        ),
        "",
        "## Results",
        "",
        (
            "| Configuration | Pass rate | Severe runs | Mean tokens / success | "
            "Token delta vs 3x5 | Mean calls | Mean candidates | Mean latency |"
        ),
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for configuration in document["manifest"]["configurations"]:
        identifier = configuration["id"]
        summary = document["summaries"][identifier]
        lines.append(
            f"| {identifier} | {summary['pass_rate']:.1%} | "
            f"{summary['runs_with_severe_mutation']}/{summary['attempts']} | "
            f"{summary['mean_total_tokens_per_successful_task']:,.0f} | "
            f"{summary['relative_token_delta_vs_reference']:+.1%} | "
            f"{summary['mean_model_calls']:.2f} | "
            f"{summary['mean_generated_candidates']:.2f} | "
            f"{summary['mean_duration_ms']:.0f} ms |"
        )
    lines.extend(
        [
            "",
            "## Selection rule",
            "",
            (
                "Choose the lowest-token configuration whose pass rate is within the "
                "pre-specified non-inferiority margin and whose severe-run rate is no "
                "worse than the 3x5 reference."
            ),
            "",
            "The chosen configuration must be revalidated against the real provider; "
            "simulator candidate diversity is deterministic.",
        ]
    )
    return "\n".join(lines) + "\n"


def load_json(path: Path) -> dict[str, Any]:
    """Load a JSON object and reject other top-level types."""
    with path.open("r", encoding="utf-8") as handle:
        value = json.load(handle)
    if not isinstance(value, dict):
        raise ValueError(f"document must be an object: {path}")
    return value


if __name__ == "__main__":
    raise SystemExit(main())
