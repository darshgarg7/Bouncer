#!/usr/bin/env python3
"""Verified project evidence."""

from __future__ import annotations

import ast
import json
import os
import subprocess
import sys
from pathlib import Path
from typing import Any, cast

ROOT = Path(__file__).resolve().parents[1]
COVERAGE_DIRECTORY = Path(os.environ.get("TMPDIR", "/tmp")) / "bouncer-coverage"


def load_object(relative: str) -> dict[str, Any]:
    """Load one repository JSON object."""
    value = cast(object, json.loads((ROOT / relative).read_text(encoding="utf-8")))
    if not isinstance(value, dict):
        raise ValueError(f"expected JSON object: {relative}")
    return cast(dict[str, Any], value)


def coverage_percent(profile: str) -> float:
    """Read the total percentage from a Go coverage profile."""
    path = COVERAGE_DIRECTORY / profile
    if not path.is_file():
        raise ValueError(f"missing coverage profile; run make coverage first: {path}")
    process = subprocess.run(
        ["go", "tool", "cover", f"-func={path}"],
        cwd=ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    for line in process.stdout.splitlines():
        if line.startswith("total:"):
            return float(line.rsplit(maxsplit=1)[1].removesuffix("%"))
    raise ValueError(f"coverage profile has no total: {path}")


def python_test_count() -> int:
    """Count unittest-style Python test methods and functions."""
    count = 0
    for path in sorted((ROOT / "tests").glob("test_*.py")):
        tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
        count += sum(
            1
            for node in ast.walk(tree)
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))
            and node.name.startswith("test_")
        )
    return count


def require(document: str, label: str, fragments: list[str]) -> None:
    """Require verified fragments in one public-facing document."""
    missing = [fragment for fragment in fragments if fragment not in document]
    if missing:
        raise ValueError(f"{label} is missing recruiter-facing evidence: {missing}")


def main() -> None:
    """Validate scan order and evidence values in the portfolio summaries."""
    readme = (ROOT / "README.md").read_text(encoding="utf-8")
    hiring = (ROOT / "docs/HIRING_GUIDE.md").read_text(encoding="utf-8")
    readme_lines = readme.splitlines()
    if len(readme_lines) > 190:
        raise ValueError(f"README exceeds the 190-line portfolio budget: {len(readme_lines)}")

    early_readme = "\n".join(readme_lines[:100])
    require(
        early_readme,
        "README first 100 lines",
        [
            "AI agents can propose actions. **They should not authorize themselves.**",
            "## At a glance",
            "## Run the demo",
            "make demo",
            "## Architecture",
            "**The model proposes. Bouncer authorizes.**",
            "## Engineering results",
        ],
    )
    require(
        readme,
        "README ownership section",
        ["independent personal engineering project by [Darsh Garg]"],
    )
    headings = [
        "## At a glance",
        "## Run the demo",
        "## Architecture",
        "## Engineering results",
        "## Hard problems I had to solve",
        "## Engineering quality",
        "## Ownership",
        "## Limitations",
        "## Documentation",
    ]
    positions = [readme.index(heading) for heading in headings]
    if positions != sorted(positions):
        raise ValueError("README recruiter-case-study sections are out of order")

    pilot = load_object("benchmarks/reports/nvidia-hosted-pilot-2026-07-27/summary.json")
    mechanism = load_object("benchmarks/reports/mechanism-results.json")
    summaries = mechanism.get("summaries")
    if not isinstance(summaries, dict):
        raise ValueError("mechanism report summaries are missing")
    single = summaries.get("single_policy")
    fixed = summaries.get("fixed_3x3")
    if not isinstance(single, dict) or not isinstance(fixed, dict):
        raise ValueError("mechanism report is missing single or fixed summaries")

    attempts = pilot.get("attempts")
    successes = pilot.get("successes")
    if not isinstance(attempts, int) or not isinstance(successes, int):
        raise ValueError("hosted pilot attempt counts are invalid")
    single_tokens = single.get("mean_total_tokens_per_successful_task")
    fixed_tokens = fixed.get("mean_total_tokens_per_successful_task")
    if not isinstance(single_tokens, (int, float)) or not isinstance(fixed_tokens, (int, float)):
        raise ValueError("mechanism token summaries are invalid")

    tests = python_test_count()
    overall = coverage_percent("all.out")
    critical = {
        "policy": coverage_percent("policy.out"),
        "router": coverage_percent("router.out"),
        "executor": coverage_percent("executor.out"),
        "anomaly": coverage_percent("anomaly.out"),
    }
    token_ratio = fixed_tokens / single_tokens
    evidence = [
        "100,000",
        f"{successes}/{attempts}",
        f"{overall:.1f}%",
        f"{token_ratio:.2f}\N{MULTIPLICATION SIGN}",
    ]
    require(readme, "README", [*evidence, f"{tests} Python tests"])
    require(hiring, "candidate brief", evidence)
    for package, percent in critical.items():
        minimum = 81 if package == "executor" and sys.platform.startswith("linux") else 90
        if percent < minimum:
            raise ValueError(
                f"critical portfolio boundary fell below {minimum}%: {package}={percent:.1f}%"
            )

    print(
        "portfolio check passed: "
        f"{len(readme_lines)} README lines, {tests} Python tests, {overall:.1f}% Go coverage, "
        f"{successes}/{attempts} hosted pilot, {token_ratio:.2f}x ensemble token ratio"
    )


if __name__ == "__main__":
    main()
