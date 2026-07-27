"""Known-truth simulator for constrained multi-objective routing tests."""

from __future__ import annotations

import random
from dataclasses import dataclass


@dataclass(frozen=True)
class Arm:
    """One contextual action with known expected outcomes."""

    candidate_id: str
    progress: float
    success: float
    latency_ms: float
    cost_units: float
    adverse_risk: float
    safe: bool = True

    def objectives(self) -> dict[str, float]:
        """Return the vector used by Pareto evaluation."""
        return {
            "progress": self.progress,
            "success": self.success,
            "latency_ms": self.latency_ms,
            "cost_units": self.cost_units,
            "adverse_risk": self.adverse_risk,
        }


class Simulator:
    """Sample delayed outcomes while retaining exact expected values."""

    def __init__(self, *, seed: int = 42, drift_after: int | None = None) -> None:
        self.randomizer = random.Random(seed)
        self.drift_after = drift_after
        self.steps = 0

    def candidates(self) -> list[Arm]:
        """Return a safe Pareto trade-off set plus dominated and unsafe arms."""
        drift = 0.15 if self.drift_after is not None and self.steps >= self.drift_after else 0.0
        return [
            Arm("fast", 0.65 - drift, 0.62, 10, 0.8, 0.08),
            Arm("balanced", 0.78, 0.8 - drift, 25, 0.45, 0.04),
            Arm("safe", 0.68, 0.74, 45, 0.25, 0.01),
            Arm("dominated", 0.4, 0.4, 80, 1.0, 0.2),
            Arm("unsafe", 0.95, 0.95, 5, 0.1, 0.6, safe=False),
        ]

    def execute(self, arm: Arm) -> dict[str, float | bool]:
        """Sample trusted outcomes for one policy-admitted arm."""
        if not arm.safe:
            raise ValueError("simulator policy rejected the unsafe arm")
        self.steps += 1
        return {
            "progress": self.randomizer.random() < arm.progress,
            "success": self.randomizer.random() < arm.success,
            "latency_ms": max(self.randomizer.gauss(arm.latency_ms, arm.latency_ms * 0.08), 0),
            "cost_units": max(self.randomizer.gauss(arm.cost_units, arm.cost_units * 0.05), 0),
            "adverse": self.randomizer.random() < arm.adverse_risk,
        }
