"""Backward-compatible re-export of the canonical samplers.

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
