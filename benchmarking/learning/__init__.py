"""Offline learning and evaluation for Bouncer's constrained router.

Nothing in this package authorizes or executes actions. Training produces
immutable artifacts that the Go control plane validates before inference.
"""

from .features import FEATURE_NAMES, FEATURE_SCHEMA_VERSION

__all__ = ["FEATURE_NAMES", "FEATURE_SCHEMA_VERSION"]
