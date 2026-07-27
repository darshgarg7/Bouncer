"""Run and analyze Bouncer's deterministic minimum viable benchmark.

This study is intentionally synthetic. It checks integration behavior and
frozen decision rules before spending provider budget on a real-model study.
"""

from __future__ import annotations

import argparse
import json
import math
import os
import random
import statistics
import subprocess
import time
from collections.abc import Iterable, Sequence
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from .baselines import run_baseline
from .mock_nim import start_mock_nim

ROOT = Path(__file__).resolve().parents[1]


def main(argv: list[str] | None = None) -> int:
    """Run the complete synthetic matrix and write JSON and Markdown reports."""
    parser = argparse.ArgumentParser(description="Run Bouncer's synthetic MVB")
    parser.add_argument(
        "--analysis-manifest",
        default="benchmarks/analysis-manifest.json",
    )
    parser.add_argument(
        "--results",
        default="benchmarks/reports/synthetic-mvb-results.json",
    )
    parser.add_argument(
        "--report",
        default="benchmarks/reports/synthetic-mvb.md",
    )
    arguments = parser.parse_args(argv)
    manifest_path = ROOT / arguments.analysis_manifest
    with manifest_path.open("r", encoding="utf-8") as handle:
        manifest = json.load(handle)
    validate_analysis_manifest(manifest)

    # All conditions share the same tasks and seeds. Keeping that pairing
    # explicit is important for the bootstrap comparisons below.
    task_paths = sorted(ROOT.glob(manifest["task_glob"]))
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
        for seed in manifest["seeds"]:
            for task_path in task_paths:
                records.append(
                    compact_record(
                        run_baseline(
                            str(task_path),
                            seed,
                            server.endpoint,
                            "langgraph",
                            dag_path=str(ROOT / "configs/skill_dag.json"),
                        )
                    )
                )
                records.append(
                    compact_record(
                        run_baseline(
                            str(task_path),
                            seed,
                            server.endpoint,
                            "structured",
                            dag_path=str(ROOT / "configs/skill_dag.json"),
                        )
                    )
                )
                records.append(
                    compact_record(
                        run_bouncer(
                            binary,
                            task_path,
                            seed,
                            server.endpoint,
                            manifest_path=ROOT / "configs/run-manifest.synthetic-v1.json",
                            extra_args=("-routing-strategy", "legacy_crowding"),
                        )
                    )
                )

    # Reports are derived from compact per-run records rather than manually
    # transcribed tables, which keeps the narrative tied to raw outcomes.
    summaries = {
        condition: summarize([record for record in records if record["condition"] == condition])
        for condition in manifest["conditions"]
    }
    comparisons = compare_conditions(records, summaries, manifest)
    duration_seconds = round(time.perf_counter() - started, 3)
    generated_at = datetime.now(UTC).isoformat()
    result_document = {
        "schema_version": "0.1.0",
        "evaluation_id": manifest["evaluation_id"],
        "generated_at": generated_at,
        "duration_seconds": duration_seconds,
        "analysis_manifest": manifest,
        "summaries": summaries,
        "comparisons": comparisons,
        "records": sorted(
            records,
            key=lambda item: (item["condition"], item["task_id"], item["seed"]),
        ),
    }
    results_path = ROOT / arguments.results
    report_path = ROOT / arguments.report
    results_path.parent.mkdir(parents=True, exist_ok=True)
    results_path.write_text(
        json.dumps(result_document, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    report_path.write_text(render_report(result_document), encoding="utf-8")
    print(
        json.dumps(
            {
                "results": str(results_path),
                "report": str(report_path),
                "summaries": summaries,
                "comparisons": comparisons,
            },
            indent=2,
        )
    )
    return 0


def validate_analysis_manifest(manifest: dict[str, Any]) -> None:
    """Reject analysis settings that would change the frozen comparison."""
    if manifest.get("schema_version") != "0.1.0":
        raise ValueError("analysis manifest schema_version must be 0.1.0")
    if manifest.get("conditions") != ["langgraph", "structured", "bouncer"]:
        raise ValueError("analysis manifest conditions are not frozen")
    seeds = manifest.get("seeds")
    if not isinstance(seeds, list) or len(seeds) < 5 or len(set(seeds)) != len(seeds):
        raise ValueError("analysis manifest requires at least five unique seeds")


def run_bouncer(
    binary: Path,
    task_path: Path,
    seed: int,
    endpoint: str,
    *,
    manifest_path: str | Path = "configs/run-manifest.example.json",
    timeout_seconds: float = 60,
    api_key: str = "",
    extra_args: Sequence[str] = (),
) -> dict[str, Any]:
    """Run the Go control loop once and decode its JSON result."""
    process_environment = os.environ.copy()
    if api_key:
        process_environment["NIM_API_KEY"] = api_key
    process = subprocess.run(
        [
            str(binary),
            "-manifest",
            str(manifest_path),
            "-task",
            str(task_path),
            "-endpoint",
            endpoint,
            "-project-root",
            str(ROOT),
            "-seed",
            str(seed),
            *extra_args,
        ],
        cwd=ROOT,
        text=True,
        capture_output=True,
        check=False,
        timeout=timeout_seconds,
        env=process_environment,
    )
    if process.returncode != 0:
        raise RuntimeError(
            f"Bouncer failed for {task_path.name} seed {seed}: {process.stderr.strip()}"
        )
    result = json.loads(process.stdout)
    if not isinstance(result, dict):
        raise ValueError("Bouncer result must be a JSON object")
    return result


def compact_record(record: dict[str, Any]) -> dict[str, Any]:
    """Keep only fields used by aggregate analysis and published reports."""
    fields = (
        "condition",
        "task_id",
        "seed",
        "passed",
        "task_complete",
        "oracle_failures",
        "turns",
        "model_calls",
        "prompt_tokens",
        "completion_tokens",
        "reasoning_tokens",
        "total_tokens",
        "generated_candidates",
        "constraint_rejections",
        "executed_actions",
        "severe_mutations",
        "duration_ms",
    )
    return {field: record[field] for field in fields}


def summarize(records: list[dict[str, Any]]) -> dict[str, Any]:
    """Aggregate success, safety, token, and latency metrics for a condition."""
    successful = [record for record in records if record["passed"]]
    severe_runs = [record for record in records if record["severe_mutations"] > 0]
    return {
        "attempts": len(records),
        "successes": len(successful),
        "pass_rate": round(len(successful) / len(records), 4),
        "runs_with_severe_mutation": len(severe_runs),
        "runs_with_severe_mutation_rate": round(len(severe_runs) / len(records), 4),
        "severe_mutations": sum(record["severe_mutations"] for record in records),
        "mean_total_tokens_per_successful_task": round(
            statistics.fmean(record["total_tokens"] for record in successful), 2
        )
        if successful
        else None,
        "median_total_tokens_per_successful_task": round(
            statistics.median(record["total_tokens"] for record in successful), 2
        )
        if successful
        else None,
        "mean_model_calls": round(statistics.fmean(record["model_calls"] for record in records), 2),
        "mean_generated_candidates": round(
            statistics.fmean(record["generated_candidates"] for record in records), 2
        ),
        "mean_constraint_rejections": round(
            statistics.fmean(record["constraint_rejections"] for record in records), 2
        ),
        "mean_duration_ms": round(statistics.fmean(record["duration_ms"] for record in records), 2),
        "p95_duration_ms": percentile([record["duration_ms"] for record in records], 0.95),
    }


def compare_conditions(
    records: list[dict[str, Any]],
    summaries: dict[str, dict[str, Any]],
    manifest: dict[str, Any],
) -> dict[str, Any]:
    """Apply the frozen paired tests and choose the next-step decision."""
    baseline_name = manifest["primary_baseline"]
    baseline = summaries[baseline_name]
    bouncer = summaries["bouncer"]
    baseline_tokens = baseline["mean_total_tokens_per_successful_task"]
    bouncer_tokens = bouncer["mean_total_tokens_per_successful_task"]
    relative_token_delta = (
        bouncer_tokens / baseline_tokens - 1
        if baseline_tokens not in {None, 0} and bouncer_tokens is not None
        else None
    )
    baseline_severe_rate = baseline["runs_with_severe_mutation_rate"]
    severe_reduction = (
        1 - bouncer["runs_with_severe_mutation_rate"] / baseline_severe_rate
        if baseline_severe_rate > 0
        else None
    )
    paired = pair_records(records, baseline_name, "bouncer")
    bootstrap = manifest["bootstrap"]
    token_differences = [
        bouncer_record["total_tokens"] - baseline_record["total_tokens"]
        for baseline_record, bouncer_record in paired
        if baseline_record["passed"] and bouncer_record["passed"]
    ]
    severe_differences = [
        float(
            int(bouncer_record["severe_mutations"] > 0)
            - int(baseline_record["severe_mutations"] > 0)
        )
        for baseline_record, bouncer_record in paired
    ]
    token_ci = (
        bootstrap_mean_ci(
            token_differences,
            bootstrap["samples"],
            bootstrap["confidence"],
            bootstrap["seed"],
        )
        if token_differences
        else None
    )
    severe_ci = bootstrap_mean_ci(
        severe_differences,
        bootstrap["samples"],
        bootstrap["confidence"],
        bootstrap["seed"] + 1,
    )
    h1_config = manifest["hypotheses"]["h1"]
    h2_config = manifest["hypotheses"]["h2"]
    pass_delta = bouncer["pass_rate"] - baseline["pass_rate"]
    h1_supported = (
        relative_token_delta is not None
        and token_ci is not None
        and relative_token_delta <= h1_config["maximum_relative_delta"]
        and pass_delta >= h1_config["pass_rate_noninferiority_margin"]
        and token_ci[1] < 0
    )
    h2_supported = (
        severe_reduction is not None
        and severe_reduction >= h2_config["minimum_relative_reduction"]
        and pass_delta >= h2_config["pass_rate_noninferiority_margin"]
        and severe_ci[1] < 0
    )
    if h1_supported and h2_supported:
        decision = "proceed_to_causal_tranche"
    elif h2_supported:
        decision = "position_as_safety_control_plane_and_optimize_cost"
    elif h1_supported:
        decision = "stop_safety_claim_and_inspect_escaped_constraints"
    else:
        decision = "pivot_or_terminate"
    return {
        "primary_baseline": baseline_name,
        "paired_runs": len(paired),
        "pass_rate_delta": round(pass_delta, 4),
        "relative_token_delta": round(relative_token_delta, 4)
        if relative_token_delta is not None
        else None,
        "mean_paired_token_difference": round(statistics.fmean(token_differences), 2)
        if token_differences
        else None,
        "mean_paired_token_difference_95ci": [round(value, 2) for value in token_ci]
        if token_ci is not None
        else None,
        "severe_mutation_relative_reduction": round(severe_reduction, 4)
        if severe_reduction is not None
        else None,
        "severe_run_rate_difference": round(statistics.fmean(severe_differences), 4),
        "severe_run_rate_difference_95ci": [round(value, 4) for value in severe_ci],
        "h1_supported_in_simulation": h1_supported,
        "h2_supported_in_simulation": h2_supported,
        "decision": decision,
    }


def pair_records(
    records: list[dict[str, Any]], left: str, right: str
) -> list[tuple[dict[str, Any], dict[str, Any]]]:
    """Pair two conditions by task identifier and random seed."""
    by_condition = {
        condition: {
            (record["task_id"], record["seed"]): record
            for record in records
            if record["condition"] == condition
        }
        for condition in (left, right)
    }
    keys = sorted(set(by_condition[left]).intersection(by_condition[right]))
    return [(by_condition[left][key], by_condition[right][key]) for key in keys]


def bootstrap_mean_ci(
    values: Sequence[float], samples: int, confidence: float, seed: int
) -> tuple[float, float]:
    """Estimate a percentile-bootstrap interval for a paired mean."""
    if not values:
        return (math.nan, math.nan)
    randomizer = random.Random(seed)
    means = []
    for _ in range(samples):
        means.append(statistics.fmean(randomizer.choice(values) for _ in range(len(values))))
    means.sort()
    alpha = (1 - confidence) / 2
    return (percentile(means, alpha), percentile(means, 1 - alpha))


def percentile(values: Iterable[float], quantile: float) -> float:
    """Return a linearly interpolated quantile from an iterable of values."""
    ordered = sorted(values)
    if not ordered:
        return math.nan
    position = (len(ordered) - 1) * quantile
    lower = math.floor(position)
    upper = math.ceil(position)
    if lower == upper:
        return round(float(ordered[lower]), 2)
    weight = position - lower
    return round(float(ordered[lower] * (1 - weight) + ordered[upper] * weight), 2)


def render_report(document: dict[str, Any]) -> str:
    """Render the synthetic evaluation and its evidence boundaries."""
    summaries = document["summaries"]
    comparison = document["comparisons"]
    h1 = "SUPPORTED" if comparison["h1_supported_in_simulation"] else "NOT SUPPORTED"
    h2 = "SUPPORTED" if comparison["h2_supported_in_simulation"] else "NOT SUPPORTED"
    run_count = sum(summary["attempts"] for summary in summaries.values())
    h1_result = (
        f"Bouncer's mean synthetic token use changed by "
        f"{comparison['relative_token_delta']:+.1%} against the LangGraph baseline. "
        f"The paired mean difference was "
        f"{comparison['mean_paired_token_difference']:+,.0f} tokens with a bootstrap "
        f"95% interval of "
        f"[{comparison['mean_paired_token_difference_95ci'][0]:+,.0f}, "
        f"{comparison['mean_paired_token_difference_95ci'][1]:+,.0f}]."
    )
    h2_result = (
        f"The severe-mutation run rate changed by "
        f"{comparison['severe_run_rate_difference']:+.1%}; the bootstrap 95% interval "
        f"was [{comparison['severe_run_rate_difference_95ci'][0]:+.1%}, "
        f"{comparison['severe_run_rate_difference_95ci'][1]:+.1%}]. Bouncer's relative "
        f"reduction was {comparison['severe_mutation_relative_reduction']:.1%}."
    )
    lines = [
        "# Bouncer Synthetic MVB Evaluation",
        "",
        f"**Evaluation:** `{document['evaluation_id']}`",
        f"**Generated:** {document['generated_at']}",
        f"**Runs:** {run_count} across 10 tasks, 5 seeds, and 3 conditions",
        f"**Wall time:** {document['duration_seconds']} seconds",
        "",
        (
            "> This is controlled integration evidence, not Nemotron, production-safety, "
            "or causal evidence. The local NIM-compatible simulator uses deterministic "
            "scenarios, approximate synthetic token accounting, and deliberately injected "
            "virtual hazards."
        ),
        "",
        "## Results",
        "",
        (
            "| Condition | Pass rate | Severe-mutation runs | Mean tokens / success | "
            "Mean model calls | Mean candidates | Mean latency |"
        ),
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for condition in ("langgraph", "structured", "bouncer"):
        summary = summaries[condition]
        lines.append(
            f"| {condition} | {summary['pass_rate']:.1%} | "
            f"{summary['runs_with_severe_mutation']}/{summary['attempts']} "
            f"({summary['runs_with_severe_mutation_rate']:.1%}) | "
            f"{summary['mean_total_tokens_per_successful_task']:,.0f} | "
            f"{summary['mean_model_calls']:.2f} | "
            f"{summary['mean_generated_candidates']:.2f} | "
            f"{summary['mean_duration_ms']:.0f} ms |"
        )
    lines.extend(
        [
            "",
            "## Hypothesis decisions",
            "",
            f"### H1 — Token efficiency: {h1}",
            "",
            h1_result,
            "",
            f"### H2 — State safety: {h2}",
            "",
            h2_result,
            "",
            "## Decision",
            "",
            f"`{comparison['decision']}`",
            "",
            (
                "The synthetic result supports the deterministic policy boundary, not the "
                "original ensemble. The policy-held-constant follow-up makes one proposer + "
                "policy the default. This study does not support the token-reduction hypothesis "
                "for the historical 3x5 configuration."
            ),
            "",
            "## What the run established",
            "",
            "- all three conditions executed the same ten task fixtures from identical state;",
            "- both baselines and Bouncer used the same deterministic NIM-compatible policy;",
            (
                "- Bouncer replayed the historical concurrent 3x5 budget through strict "
                "five-action parsing, canonical Go policy, the legacy crowding selector, "
                "virtual execution, and oracle scoring;"
            ),
            "- LangGraph executed the comparison agent as a real state graph;",
            (
                "- Bouncer blocked every deliberately injected out-of-root mutation in "
                "this fixture set; and"
            ),
            "- the 3x5 proposal configuration had a substantial synthetic token cost.",
            "",
            "## What the run did not establish",
            "",
            "- real model quality, diversity, tokenization, latency, or rate-limit behavior;",
            "- safety against unmodeled operations or adversarial real-world environments;",
            "- causal identification, PC structure validity, or IPW estimator quality;",
            "- Kafka overhead or distributed failure behavior; or",
            "- statistical evidence beyond the deliberately constructed smoke suite.",
            "",
            "## Required external follow-up",
            "",
            "1. Repeat the concurrency gate against a frozen Nemotron deployment.",
            "2. Run the same task-seed matrix with provider-reported token usage.",
            "3. Expand the task suite and preregister a held-out adversarial set.",
            (
                "4. Treat the current single-proposer policy baseline as primary; require "
                "real-task evidence before promoting adaptive or ensemble modes."
            ),
            "5. Begin causal simulation only after the static system survives the real-model gate.",
            "",
            "## Reproduce",
            "",
            "```bash",
            "make evaluate-synthetic",
            "```",
            "",
            (
                "The raw per-run summaries are stored in "
                "[`synthetic-mvb-results.json`](synthetic-mvb-results.json)."
            ),
        ]
    )
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    raise SystemExit(main())
