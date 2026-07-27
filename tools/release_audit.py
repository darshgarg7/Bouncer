#!/usr/bin/env python3
"""Audit documentation, evidence, claims, and credentials for publication."""

from __future__ import annotations

import hashlib
import json
import re
import subprocess
from pathlib import Path
from typing import Any, cast
from urllib.parse import unquote

from summarize_pilot import render_report as render_pilot
from summarize_pilot import summarize as summarize_pilot

from benchmarking.ablate import render_report as render_ablation
from benchmarking.evaluate import render_report as render_synthetic
from benchmarking.mechanism_evaluate import render_report as render_mechanism
from benchmarking.projector_ablate import render_report as render_projector
from benchmarking.provenance import ROOT, source_fingerprint

MARKDOWN_LINK = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
SECRET_PATTERNS = {
    "NVIDIA API key": re.compile(r"\bnvapi-[A-Za-z0-9_-]{16,}"),
    "OpenAI-style API key": re.compile(r"\bsk-[A-Za-z0-9_-]{20,}"),
    "GitHub token": re.compile(r"\b(?:ghp|github_pat)_[A-Za-z0-9_]{20,}"),
    "AWS access key": re.compile(r"\bAKIA[0-9A-Z]{16}\b"),
    "private key": re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----"),
}
REPORTS = (
    (
        Path("benchmarks/reports/synthetic-mvb-results.json"),
        Path("benchmarks/reports/synthetic-mvb.md"),
        render_synthetic,
    ),
    (
        Path("benchmarks/reports/synthetic-ablation-results.json"),
        Path("benchmarks/reports/synthetic-ablation.md"),
        render_ablation,
    ),
    (
        Path("benchmarks/reports/synthetic-projector-ablation-results.json"),
        Path("benchmarks/reports/synthetic-projector-ablation.md"),
        render_projector,
    ),
    (
        Path("benchmarks/reports/mechanism-results.json"),
        Path("benchmarks/reports/mechanism.md"),
        render_mechanism,
    ),
)
PILOT_DIRECTORY = Path("benchmarks/reports/nvidia-hosted-pilot-2026-07-27")


def tracked_paths() -> list[Path]:
    """Return every path in the Git index."""
    process = subprocess.run(["git", "ls-files", "-z"], cwd=ROOT, check=True, capture_output=True)
    return [Path(item.decode()) for item in process.stdout.split(b"\0") if item]


def check_documentation_links(paths: list[Path]) -> None:
    """Reject broken repository-relative links in tracked Markdown."""
    failures: list[str] = []
    for relative in paths:
        if relative.suffix.lower() != ".md":
            continue
        document = (ROOT / relative).read_text(encoding="utf-8")
        for match in MARKDOWN_LINK.finditer(document):
            raw_target = match.group(1).strip()
            if raw_target.startswith("<") and ">" in raw_target:
                target = raw_target[1 : raw_target.index(">")]
            else:
                target = raw_target.split(maxsplit=1)[0]
            if (
                not target
                or target.startswith("#")
                or re.match(r"^[A-Za-z][A-Za-z0-9+.-]*:", target)
            ):
                continue
            path_text = unquote(target.split("#", 1)[0].split("?", 1)[0])
            if not path_text:
                continue
            resolved = (ROOT / relative.parent / path_text).resolve()
            try:
                resolved.relative_to(ROOT)
            except ValueError:
                failures.append(f"{relative}: link escapes repository: {target}")
                continue
            if not resolved.exists():
                failures.append(f"{relative}: missing link target: {target}")
    if failures:
        raise ValueError("documentation link audit failed:\n" + "\n".join(failures))


def check_credentials(paths: list[Path]) -> None:
    """Reject tracked environment files and recognizable credential values."""
    failures: list[str] = []
    for relative in paths:
        name = relative.name
        if (name == ".env" or name.startswith(".env.")) and name != ".env.example":
            failures.append(f"tracked environment file: {relative}")
        path = ROOT / relative
        if not path.is_file() or path.stat().st_size > 16 << 20:
            continue
        try:
            content = path.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
        for label, pattern in SECRET_PATTERNS.items():
            if pattern.search(content):
                failures.append(f"{relative}: credential-shaped value ({label})")
    if failures:
        raise ValueError("credential audit failed:\n" + "\n".join(failures))


def load_object(relative: Path) -> dict[str, Any]:
    """Load one repository JSON object."""
    value = cast(object, json.loads((ROOT / relative).read_text(encoding="utf-8")))
    if not isinstance(value, dict):
        raise ValueError(f"expected JSON object: {relative}")
    return cast(dict[str, Any], value)


def resolve_pointer(document: object, pointer: str) -> object:
    """Resolve the JSON Pointer subset used by publication assertions."""
    current = document
    for encoded in pointer.removeprefix("/").split("/"):
        token = encoded.replace("~1", "/").replace("~0", "~")
        if isinstance(current, dict) and token in current:
            current = current[token]
        elif isinstance(current, list) and token.isdigit() and int(token) < len(current):
            current = current[int(token)]
        else:
            raise ValueError(f"unresolved JSON pointer {pointer}")
    return current


def check_claims() -> None:
    """Bind the public claim register to machine-readable report assertions."""
    manifest = load_object(Path("benchmarks/publication-claims.json"))
    claims = manifest.get("claims")
    if not isinstance(claims, list):
        raise ValueError("publication claims must be an array")
    claim_lines = {
        line.split("|", 2)[1].strip(): line
        for line in (ROOT / "docs/CLAIMS.md").read_text(encoding="utf-8").splitlines()
        if line.startswith("| C-")
    }
    seen: set[str] = set()
    for item in claims:
        if not isinstance(item, dict):
            raise ValueError("publication claim entry must be an object")
        claim_id = item.get("claim_id")
        artifact_text = item.get("artifact")
        if not isinstance(claim_id, str) or claim_id in seen:
            raise ValueError(f"invalid or duplicate publication claim ID: {claim_id}")
        seen.add(claim_id)
        if not isinstance(artifact_text, str):
            raise ValueError(f"missing artifact for {claim_id}")
        artifact = Path(artifact_text)
        document = load_object(artifact)
        line = claim_lines.get(claim_id)
        if line is None:
            raise ValueError(f"{claim_id} is missing from docs/CLAIMS.md")
        if artifact_text not in line:
            raise ValueError(f"{claim_id} does not cite {artifact_text} in docs/CLAIMS.md")
        required_text = item.get("required_text")
        if not isinstance(required_text, list) or any(
            not isinstance(text, str) or text not in line for text in required_text
        ):
            raise ValueError(f"{claim_id} wording is stale in docs/CLAIMS.md")
        assertions = item.get("assertions")
        if not isinstance(assertions, list):
            raise ValueError(f"{claim_id} assertions must be an array")
        for assertion in assertions:
            if not isinstance(assertion, dict):
                raise ValueError(f"{claim_id} assertion must be an object")
            pointer = assertion.get("pointer")
            if not isinstance(pointer, str):
                raise ValueError(f"{claim_id} assertion pointer is invalid")
            actual = resolve_pointer(document, pointer)
            expected = assertion.get("equals")
            if actual != expected:
                raise ValueError(
                    f"{claim_id} {artifact}:{pointer} is {actual!r}, expected {expected!r}"
                )


def check_generated_evidence() -> None:
    """Verify source bindings, calibration hashes, renderers, and pilot anchors."""
    expected_fingerprint = source_fingerprint()
    for result_path, report_path, renderer in REPORTS:
        document = load_object(result_path)
        provenance = document.get("provenance")
        if not isinstance(provenance, dict):
            raise ValueError(f"missing provenance: {result_path}")
        if provenance.get("source_fingerprint_sha256") != expected_fingerprint:
            raise ValueError(f"stale source fingerprint: {result_path}")
        calibration = provenance.get("objective_calibration")
        if not isinstance(calibration, dict) or not isinstance(calibration.get("path"), str):
            raise ValueError(f"missing objective calibration provenance: {result_path}")
        calibration_data = (ROOT / calibration["path"]).read_bytes()
        if hashlib.sha256(calibration_data).hexdigest() != calibration.get("artifact_sha256"):
            raise ValueError(f"stale calibration digest: {result_path}")
        rendered = renderer(document)
        if rendered != (ROOT / report_path).read_text(encoding="utf-8"):
            raise ValueError(f"generated report does not match its JSON artifact: {report_path}")

    pilot_path = ROOT / PILOT_DIRECTORY
    recorded = load_object(PILOT_DIRECTORY / "summary.json")
    current = summarize_pilot(pilot_path)
    for field in (
        "provider",
        "model",
        "endpoint",
        "task_ids",
        "attempts",
        "successes",
        "task_completions",
        "constraint_rejections",
        "severe_mutations",
        "total_model_calls",
        "total_tokens",
        "objective_calibration",
        "task_outcomes",
        "event_chain_anchors",
    ):
        if recorded.get(field) != current.get(field):
            raise ValueError(f"hosted pilot summary field is stale: {field}")
    source = recorded.get("source")
    if not isinstance(source, dict) or source.get("fingerprint_sha256") != expected_fingerprint:
        raise ValueError("hosted pilot source fingerprint is stale")
    if render_pilot(recorded) != (pilot_path / "README.md").read_text(encoding="utf-8"):
        raise ValueError("hosted pilot README does not match summary.json")

    status = subprocess.run(
        [
            "git",
            "status",
            "--porcelain",
            "--untracked-files=all",
            "--",
            "benchmarks/reports",
        ],
        cwd=ROOT,
        check=True,
        capture_output=True,
        text=True,
    ).stdout.strip()
    if status:
        raise ValueError("generated evidence has uncommitted changes:\n" + status)


def main() -> None:
    """Run every publication-specific audit after ordinary quality gates."""
    paths = tracked_paths()
    check_documentation_links(paths)
    check_credentials(paths)
    check_claims()
    check_generated_evidence()
    print(
        "release audit passed: documentation links, credential scan, claim assertions, "
        "source fingerprints, generated reports, and pilot anchors"
    )


if __name__ == "__main__":
    main()
