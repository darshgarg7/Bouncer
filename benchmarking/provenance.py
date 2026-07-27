"""Compute deterministic source provenance for generated evidence artifacts."""

from __future__ import annotations

import hashlib
import json
import subprocess
from pathlib import Path
from typing import Final, cast

ROOT: Final = Path(__file__).resolve().parents[1]
_SOURCE_FILES: Final = (
    Path("go.mod"),
    Path("go.sum"),
    Path("pyproject.toml"),
    Path("benchmarks/ablation-manifest.json"),
    Path("benchmarks/analysis-manifest.json"),
    Path("benchmarks/mechanism-manifest.json"),
    Path("benchmarks/projector-ablation-manifest.json"),
    Path("benchmarks/scenarios.json"),
)
_SOURCE_TREES: Final = {
    Path("cmd"): {".go"},
    Path("internal"): {".go"},
    Path("benchmarking"): {".py"},
    Path("constraint_projection"): {".py"},
    Path("configs"): {".json"},
    Path("schemas"): {".json"},
    Path("benchmarks/tasks"): {".json"},
}


def source_fingerprint(root: Path = ROOT) -> str:
    """Hash every runtime, contract, task, and evaluation source in stable order."""
    paths = set(_SOURCE_FILES)
    for directory, suffixes in _SOURCE_TREES.items():
        paths.update(
            path.relative_to(root)
            for path in (root / directory).rglob("*")
            if path.is_file() and path.suffix in suffixes and "__pycache__" not in path.parts
        )
    digest = hashlib.sha256()
    for relative in sorted(paths, key=lambda path: path.as_posix()):
        data = (root / relative).read_bytes()
        encoded_path = relative.as_posix().encode("utf-8")
        digest.update(len(encoded_path).to_bytes(8, "big"))
        digest.update(encoded_path)
        digest.update(len(data).to_bytes(8, "big"))
        digest.update(data)
    return digest.hexdigest()


def source_revision(root: Path = ROOT) -> str:
    """Return the Git commit containing the evaluated source."""
    process = subprocess.run(
        ["git", "rev-parse", "HEAD"],
        cwd=root,
        check=True,
        capture_output=True,
        text=True,
    )
    return process.stdout.strip()


def evidence_provenance(calibration_path: Path, root: Path = ROOT) -> dict[str, object]:
    """Describe the exact source and objective artifact used by one study."""
    resolved = calibration_path if calibration_path.is_absolute() else root / calibration_path
    data = resolved.read_bytes()
    document = cast(object, json.loads(data))
    if not isinstance(document, dict) or not isinstance(document.get("calibration_id"), str):
        raise ValueError(f"invalid objective calibration artifact: {resolved}")
    return {
        "source_revision": source_revision(root),
        "source_fingerprint_sha256": source_fingerprint(root),
        "objective_calibration": {
            "path": resolved.relative_to(root).as_posix(),
            "calibration_id": document["calibration_id"],
            "artifact_sha256": hashlib.sha256(data).hexdigest(),
        },
    }
