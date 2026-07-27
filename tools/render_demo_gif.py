#!/usr/bin/env python3
"""Render the live, credential-free demo output as a compact terminal GIF."""

from __future__ import annotations

import subprocess
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[1]
OUTPUT = ROOT / "docs/assets/bouncer-demo.gif"
WIDTH, HEIGHT = 1400, 780
BACKGROUND = "#0b0f14"
PANEL = "#111821"
TEXT = "#d8dee9"
MUTED = "#7f8c9d"
CYAN = "#4fd6e5"
GREEN = "#55d187"
YELLOW = "#e6c56a"


def font(size: int, bold: bool = False) -> ImageFont.FreeTypeFont | ImageFont.ImageFont:
    """Load a portable monospace font with a built-in fallback."""
    candidates = [
        "/System/Library/Fonts/SFNSMonoBold.ttf" if bold else "/System/Library/Fonts/SFNSMono.ttf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf"
        if bold
        else "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
    ]
    for candidate in candidates:
        if Path(candidate).exists():
            return ImageFont.truetype(candidate, size)
    return ImageFont.load_default()


def demo_lines() -> list[str]:
    """Run the real demo and return its stable, presentation-safe output."""
    process = subprocess.run(
        ["go", "run", "./cmd/bouncer-demo"],
        cwd=ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    lines = process.stdout.strip().splitlines()
    stable: list[str] = []
    for line in lines:
        if "final_hash=" in line:
            prefix, remainder = line.split("final_hash=", 1)
            _, suffix = remainder.split("...", 1)
            line = f"{prefix}final_hash=<verified>...{suffix}"
        stable.append(line)
    return stable


def frame(lines: list[str]) -> Image.Image:
    """Render one terminal frame."""
    image = Image.new("RGB", (WIDTH, HEIGHT), BACKGROUND)
    draw = ImageDraw.Draw(image)
    draw.rounded_rectangle(
        (45, 45, WIDTH - 45, HEIGHT - 45),
        radius=18,
        fill=PANEL,
        outline="#263341",
        width=2,
    )
    for index, color in enumerate(("#ff605c", "#ffbd44", "#00ca4e")):
        x = 78 + index * 34
        draw.ellipse((x, 72, x + 18, 90), fill=color)
    draw.text((WIDTH - 300, 68), "make demo", font=font(20), fill=MUTED)
    draw.text((78, 120), "$ make demo", font=font(25, bold=True), fill=CYAN)
    y = 172
    for line in lines:
        color = TEXT
        if line.startswith("[PASS]") or line.startswith("DEMO PASSED"):
            color = GREEN
        elif line.lstrip().startswith(("audit:", "verified audit:", "artifact=")):
            color = YELLOW
        elif line.startswith("Bouncer"):
            color = CYAN
        draw.text(
            (78, y),
            line,
            font=font(22, bold=line.startswith("DEMO PASSED")),
            fill=color,
        )
        y += 45
    return image


def main() -> None:
    """Create a short progressive GIF from actual demo output."""
    lines = demo_lines()
    cutoffs = [2, 4, 5, 7, len(lines)]
    frames = [frame(lines[:cutoff]) for cutoff in cutoffs]
    OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    frames[0].save(
        OUTPUT,
        save_all=True,
        append_images=frames[1:],
        duration=[900, 1200, 1200, 1400, 2600],
        loop=0,
        optimize=True,
    )
    print(OUTPUT)


if __name__ == "__main__":
    main()
