"""Auditable contextual-bandit challengers for offline and canary evaluation."""

from __future__ import annotations

import math
import random
from collections.abc import Mapping, Sequence
from dataclasses import dataclass


@dataclass(frozen=True)
class Choice:
    """A selected candidate with its exact behavior probability."""

    candidate_id: str
    probability: float
    exploratory: bool


def safe_epsilon_greedy(
    candidate_ids: Sequence[str],
    greedy_candidate_id: str,
    *,
    epsilon: float,
    randomizer: random.Random,
) -> Choice:
    """Explore uniformly among non-greedy members of an approved frontier."""
    unique = sorted(set(candidate_ids))
    if not unique or greedy_candidate_id not in unique:
        raise ValueError("greedy candidate must belong to a non-empty safe frontier")
    if not 0 <= epsilon <= 1 or not math.isfinite(epsilon):
        raise ValueError("epsilon must be in [0,1]")
    alternatives = [candidate_id for candidate_id in unique if candidate_id != greedy_candidate_id]
    if not alternatives:
        return Choice(greedy_candidate_id, 1.0, False)
    if randomizer.random() < epsilon:
        return Choice(
            randomizer.choice(alternatives),
            epsilon / len(alternatives),
            True,
        )
    return Choice(greedy_candidate_id, 1 - epsilon, False)


class ConservativeLinUCB:
    """Linear UCB over candidate feature vectors, never candidate identities."""

    def __init__(self, dimension: int, *, alpha: float = 0.5, ridge: float = 1.0) -> None:
        if dimension < 1 or alpha < 0 or ridge <= 0:
            raise ValueError("dimension, alpha, and ridge are invalid")
        self.dimension = dimension
        self.alpha = alpha
        self.matrix = [
            [ridge if row == column else 0.0 for column in range(dimension)]
            for row in range(dimension)
        ]
        self.reward_sum = [0.0] * dimension

    def scores(self, candidates: Mapping[str, Sequence[float]]) -> dict[str, float]:
        """Return deterministic UCB scores for an externally filtered safe set."""
        inverse = invert(self.matrix)
        theta = matrix_vector(inverse, self.reward_sum)
        result: dict[str, float] = {}
        for candidate_id, raw in candidates.items():
            vector = self._vector(raw)
            mean = dot(theta, vector)
            variance = max(dot(vector, matrix_vector(inverse, vector)), 0.0)
            result[candidate_id] = mean + self.alpha * math.sqrt(variance)
        if not result:
            raise ValueError("LinUCB requires at least one safe candidate")
        return result

    def choose(self, candidates: Mapping[str, Sequence[float]]) -> Choice:
        """Choose the maximum UCB with stable candidate-ID tie breaking."""
        scores = self.scores(candidates)
        selected = min(scores, key=lambda candidate_id: (-scores[candidate_id], candidate_id))
        return Choice(selected, 1.0, False)

    def update(self, vector: Sequence[float], reward: float) -> None:
        """Apply one observed reward; production should batch and promote state."""
        features = self._vector(vector)
        if not math.isfinite(reward):
            raise ValueError("reward must be finite")
        for row in range(self.dimension):
            self.reward_sum[row] += reward * features[row]
            for column in range(self.dimension):
                self.matrix[row][column] += features[row] * features[column]

    def _vector(self, value: Sequence[float]) -> list[float]:
        if len(value) != self.dimension:
            raise ValueError("feature vector has the wrong dimension")
        vector = [float(item) for item in value]
        if any(not math.isfinite(item) for item in vector):
            raise ValueError("feature vector must be finite")
        return vector


class LinearThompsonSampling(ConservativeLinUCB):
    """Linear Thompson Sampling challenger with a reproducible random source."""

    def __init__(
        self,
        dimension: int,
        *,
        posterior_scale: float = 0.25,
        ridge: float = 1.0,
        seed: int = 42,
    ) -> None:
        super().__init__(dimension, alpha=0.0, ridge=ridge)
        if posterior_scale < 0 or not math.isfinite(posterior_scale):
            raise ValueError("posterior_scale must be finite and non-negative")
        self.posterior_scale = posterior_scale
        self.randomizer = random.Random(seed)

    def choose(self, candidates: Mapping[str, Sequence[float]]) -> Choice:
        """Sample a linear policy and report its replayable deterministic choice."""
        inverse = invert(self.matrix)
        mean = matrix_vector(inverse, self.reward_sum)
        factor = cholesky(inverse)
        noise = [self.randomizer.gauss(0, 1) for _ in range(self.dimension)]
        sampled = [
            mean[row]
            + self.posterior_scale
            * sum(factor[row][column] * noise[column] for column in range(row + 1))
            for row in range(self.dimension)
        ]
        scores = {
            candidate_id: dot(sampled, self._vector(vector))
            for candidate_id, vector in candidates.items()
        }
        if not scores:
            raise ValueError("Thompson sampling requires at least one safe candidate")
        selected = min(scores, key=lambda candidate_id: (-scores[candidate_id], candidate_id))
        # A sampled policy's marginal propensity is not available in closed form.
        # Keep it out of production OPE until Monte Carlo propensities are frozen.
        return Choice(selected, math.nan, True)


def dot(left: Sequence[float], right: Sequence[float]) -> float:
    """Compute a strict vector dot product."""
    if len(left) != len(right):
        raise ValueError("dot product dimensions differ")
    return sum(a * b for a, b in zip(left, right, strict=True))


def matrix_vector(matrix: Sequence[Sequence[float]], vector: Sequence[float]) -> list[float]:
    """Multiply a square matrix by a vector."""
    return [dot(row, vector) for row in matrix]


def invert(matrix: Sequence[Sequence[float]]) -> list[list[float]]:
    """Invert a positive-definite matrix with pivoted elimination."""
    size = len(matrix)
    if size == 0 or any(len(row) != size for row in matrix):
        raise ValueError("matrix must be non-empty and square")
    augmented = [
        [*map(float, row), *[float(index == column) for column in range(size)]]
        for index, row in enumerate(matrix)
    ]
    for column in range(size):
        pivot = max(range(column, size), key=lambda row: abs(augmented[row][column]))
        if abs(augmented[pivot][column]) < 1e-12:
            raise ValueError("matrix is singular")
        augmented[column], augmented[pivot] = augmented[pivot], augmented[column]
        divisor = augmented[column][column]
        augmented[column] = [value / divisor for value in augmented[column]]
        for row in range(size):
            if row == column:
                continue
            factor = augmented[row][column]
            augmented[row] = [
                value - factor * pivot_value
                for value, pivot_value in zip(augmented[row], augmented[column], strict=True)
            ]
    return [row[size:] for row in augmented]


def cholesky(matrix: Sequence[Sequence[float]]) -> list[list[float]]:
    """Return a lower Cholesky factor for a positive-definite matrix."""
    size = len(matrix)
    result = [[0.0] * size for _ in range(size)]
    for row in range(size):
        for column in range(row + 1):
            residual = matrix[row][column] - sum(
                result[row][index] * result[column][index] for index in range(column)
            )
            if row == column:
                result[row][column] = math.sqrt(max(residual, 1e-12))
            else:
                result[row][column] = residual / result[column][column]
    return result
