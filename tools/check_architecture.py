#!/usr/bin/env python3
"""Enforce dependency direction around Bouncer's authoritative runtime."""

from __future__ import annotations

import json
import subprocess
from typing import Any, cast

FORBIDDEN: dict[str, set[str]] = {
    "bouncer/internal/action": {
        "bouncer/internal/policy",
        "bouncer/internal/router",
        "bouncer/internal/executor",
        "bouncer/internal/control",
        "bouncer/internal/learning",
    },
    "bouncer/internal/policy": {
        "bouncer/internal/router",
        "bouncer/internal/executor",
        "bouncer/internal/control",
        "bouncer/internal/learning",
        "bouncer/internal/nimclient",
    },
    "bouncer/internal/calibration": {
        "bouncer/internal/policy",
        "bouncer/internal/executor",
        "bouncer/internal/control",
        "bouncer/internal/learning",
    },
    "bouncer/internal/learning": {
        "bouncer/internal/policy",
        "bouncer/internal/executor",
        "bouncer/internal/control",
        "bouncer/internal/nimclient",
    },
    "bouncer/internal/router": {
        "bouncer/internal/policy",
        "bouncer/internal/executor",
        "bouncer/internal/control",
        "bouncer/internal/nimclient",
    },
    "bouncer/internal/executor": {
        "bouncer/internal/router",
        "bouncer/internal/learning",
        "bouncer/internal/control",
        "bouncer/internal/nimclient",
    },
}


def packages() -> list[dict[str, Any]]:
    """Read package metadata from the Go toolchain."""
    process = subprocess.run(
        ["go", "list", "-json", "./internal/..."],
        check=True,
        capture_output=True,
        text=True,
    )
    decoder = json.JSONDecoder()
    remaining = process.stdout.lstrip()
    result: list[dict[str, Any]] = []
    while remaining:
        value, offset = decoder.raw_decode(remaining)
        if not isinstance(value, dict):
            raise ValueError("go list returned a non-object package")
        result.append(cast(dict[str, Any], value))
        remaining = remaining[offset:].lstrip()
    return result


def main() -> None:
    """Reject imports that invert a declared trust-boundary dependency."""
    violations: list[str] = []
    for package in packages():
        path = package.get("ImportPath")
        imports = package.get("Imports", [])
        if not isinstance(path, str) or not isinstance(imports, list):
            raise ValueError("go list package metadata is malformed")
        forbidden = FORBIDDEN.get(path, set())
        for imported in imports:
            if isinstance(imported, str) and imported in forbidden:
                violations.append(f"{path} must not import {imported}")
    if violations:
        raise SystemExit("architecture dependency check failed:\n" + "\n".join(violations))
    print(f"architecture dependency check passed for {len(FORBIDDEN)} boundary packages")


if __name__ == "__main__":
    main()
