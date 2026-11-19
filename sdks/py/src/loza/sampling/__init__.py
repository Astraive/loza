"""Backward-compatible re-export of the canonical samplers.
from __future__ import annotations

import math
import random
from datetime import timedelta
from typing import Callable, Any

from ..core.event import EventContext

Sampler = Callable[[EventContext], bool]


def sample_all() -> Sampler:
    return lambda event: True


def sample_none() -> Sampler:
    return lambda event: False


def sample_random(rate: float) -> Sampler:
    if not math.isfinite(rate):
        raise ValueError(f"sampling rate must be finite, got {rate!r}")
    bounded = max(0.0, min(1.0, rate))
    return lambda event: random.random() < bounded


def sample_errors() -> Sampler:
    return lambda event: event.outcome == "error" or event.error is not None


def sample_slow_requests(duration: timedelta) -> Sampler:
    threshold = int(duration.total_seconds() * 1000)
    return lambda event: event.duration_ms() >= threshold

This module used to contain a second, byte-for-byte copy of the sampler
factories defined in :mod:`loza.core.sampler`.  Keeping the implementation in a
single place prevents the two copies from drifting, so every public name here
is imported directly from ``loza.core.sampler``.
"""

from __future__ import annotations

from ..core.sampler import *  # noqa: F401,F403

__all__ = [
    "Sampler",
    "sample_all",
    "sample_none",
    "sample_random",
    "sample_rate",
    "sample_errors",
    "sample_slow_requests",
    "sample_status_codes",
    "sample_routes",
    "sample_users",
    "sample_tenants",
    "sample_feature_flag",
    "sample_by_header",
    "any_sampler",
    "all_sampler",
    "not_sampler",
    "sample_rate_limited",
    "sample_by_event",
    "sample_by_outcome",
    "should_sample",
    "allow_fields",
    "block_fields",
    "SampleByEvent",
    "SampleByOutcome",
    "ShouldSample",
    "AllowFields",
    "BlockFields",
]
