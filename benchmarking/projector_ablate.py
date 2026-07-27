from __future__ import annotations

import argparse
import json
import subprocess
import time
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from .ablate import load_json, run_variant
from .evaluate import compact_record, summarize
from .mock_nim import start_mock_nim

ROOT = Path(__file__).resolve().parents[1]


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Run Bouncer projector lifecycle ablation")
    parser.add_argument(
        "--manifest",
        default="benchmarks/projector-ablation-manifest.json",
    )
    parser.add_argument(
        "--results",
        default="benchmarks/reports/synthetic-projector-ablation-results.json",
    )
    parser.add_argument(
        "--report",
        default="benchmarks/reports/synthetic-projector-ablation.md",
    )
    arguments = parser.parse_args(argv)
    manifest = load_json(ROOT / arguments.manifest)
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
        for mode in manifest["modes"]:
            configuration = {
                "id": mode,
                "proposers": manifest["proposers"],
                "beam_width": manifest["beam_width"],
                "projector_mode": mode,
            }
            for seed in manifest["seeds"]:
                for task_path in task_paths:
                    record = compact_record(
                        run_variant(binary, task_path, seed, server.endpoint, configuration)
                    )
                    record["projector_mode"] = mode
                    records.append(record)
    summaries = {
        mode: summarize([record for record in records if record["projector_mode"] == mode])
        for mode in manifest["modes"]
    }
    subprocess_latency = summaries["subprocess"]["mean_duration_ms"]
    persistent_latency = summaries["persistent"]["mean_duration_ms"]
    speedup = round(subprocess_latency / persistent_latency, 2)
    document = {
        "schema_version": "0.1.0",
        "evaluation_id": manifest["evaluation_id"],
        "generated_at": datetime.now(UTC).isoformat(),
        "duration_seconds": round(time.perf_counter() - started, 3),
        "manifest": manifest,
        "summaries": summaries,
        "latency_speedup": speedup,
        "selected_mode": "persistent"
        if summaries["persistent"]["pass_rate"] == summaries["subprocess"]["pass_rate"]
        and summaries["persistent"]["runs_with_severe_mutation_rate"]
        <= summaries["subprocess"]["runs_with_severe_mutation_rate"]
        and persistent_latency < subprocess_latency
        else "subprocess",
        "records": sorted(
            records,
            key=lambda item: (item["projector_mode"], item["task_id"], item["seed"]),
        ),
    }
    results_path = ROOT / arguments.results
    report_path = ROOT / arguments.report
    results_path.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    report_path.write_text(render_report(document), encoding="utf-8")
    print(
        json.dumps(
            {
                "results": str(results_path),
                "report": str(report_path),
                "selected_mode": document["selected_mode"],
                "latency_speedup": speedup,
                "summaries": summaries,
            },
            indent=2,
        )
    )
    return 0


def render_report(document: dict[str, Any]) -> str:
    lines = [
        "# Bouncer Synthetic Projector Lifecycle Ablation",
        "",
        f"**Evaluation:** `{document['evaluation_id']}`",
        f"**Generated:** {document['generated_at']}",
        f"**Selected mode:** `{document['selected_mode']}`",
        f"**Mean latency speedup:** {document['latency_speedup']:.2f}x",
        "",
        (
            "> Both modes use the identical JSON batch protocol and projector logic. "
            "Only Python process lifetime changes."
        ),
        "",
        "| Mode | Pass rate | Severe runs | Mean tokens | Mean latency | p95 latency |",
        "| --- | ---: | ---: | ---: | ---: | ---: |",
    ]
    for mode in document["manifest"]["modes"]:
        summary = document["summaries"][mode]
        lines.append(
            f"| {mode} | {summary['pass_rate']:.1%} | "
            f"{summary['runs_with_severe_mutation']}/{summary['attempts']} | "
            f"{summary['mean_total_tokens_per_successful_task']:,.0f} | "
            f"{summary['mean_duration_ms']:.0f} ms | {summary['p95_duration_ms']:.0f} ms |"
        )
    lines.extend(
        [
            "",
            "The persistent worker is selected only if pass rate and safety do not regress.",
        ]
    )
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    raise SystemExit(main())
