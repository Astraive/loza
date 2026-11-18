"""Regression tests for the ``loza.sampling`` compatibility re-export.

The module used to hold a second copy of every sampler in ``loza.core.sampler``.
These tests pin the single-source-of-truth contract: each public sampler
exposed by ``loza.sampling`` must be the *same object* as its canonical
counterpart in ``loza.core.sampler``.
"""

from __future__ import annotations

import inspect

import loza.core.sampler as core_sampler
import loza.sampling as sampling

SAMPLER_NAMES = (
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
)


def test_sampling_reexports_canonical_samplers() -> None:
    for name in SAMPLER_NAMES:
        assert hasattr(core_sampler, name), f"canonical sampler {name} is missing"
        assert getattr(sampling, name) is getattr(core_sampler, name), (
            f"loza.sampling.{name} is a second implementation; re-export loza.core.sampler.{name}"
        )


def test_sampling_has_no_inline_sampler_definitions() -> None:
    """The compatibility module must not define sampler functions itself."""
    for name in SAMPLER_NAMES:
        obj = getattr(sampling, name)
        if inspect.isfunction(obj):
            assert obj.__module__ == core_sampler.__name__


def test_sampling_exposes_core_only_samplers() -> None:
    """Samplers added to core after the duplicate was written are available here."""
    assert callable(sampling.sample_rate_limited(2, window=60))
    assert callable(sampling.sample_by_event(lambda event: True))
