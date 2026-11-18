"""Regression tests for the sampler benchmark (issue #781)."""

from __future__ import annotations

import os
import sys

import loza

# The benchmark lives in sdks/py/bench, which is not part of the installed
# package. Add it to sys.path so the real benchmark function is exercised.
_BENCH_DIR = os.path.join(os.path.dirname(__file__), "..", "bench")
if _BENCH_DIR not in sys.path:
    sys.path.insert(0, _BENCH_DIR)

import sampler_bench  # noqa: E402


class _CountingSampler:
    """Sampler that records each decision so we can prove it was exercised."""

    def __init__(self) -> None:
        self.calls = 0

    def __call__(self, ctx) -> bool:
        self.calls += 1
        return True


def test_sampler_bench_exercises_configured_sampler():
    """The sampler passed to _run_sampler_bench must actually drive sampling.

    Before the fix, finish/emit used the module-level facades which delegate to
    the global default logger, so the configured sampler was never called and
    every benchmark variant measured the default logger instead.
    """
    sampler = _CountingSampler()
    iterations = 32

    result = sampler_bench._run_sampler_bench("counting", sampler, iterations=iterations)

    assert sampler.calls == iterations
    assert result["iterations"] == iterations
