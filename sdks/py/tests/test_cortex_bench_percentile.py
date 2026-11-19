"""Regression tests for percentile computation in the cortex benchmark.

The benchmark computed its p95/p99 with ``sorted(v)[int(len(v) * pct)]``.
With the small sample sizes it collects (10 fast, 5 deep) that index always
resolves to the final element, so the reported percentile was the sample
maximum and the <=2s/<=6s PASS/FAIL checks were decided by a single worst
sample. These tests pin the corrected interpolated-percentile behavior.
"""

import pytest

from bench.cortex_bench import _percentile

FAST_SAMPLE_SIZE = 10
DEEP_SAMPLE_SIZE = 5


def _old_percentile(values, pct):
    """The buggy formula the fix replaces (kept only to reproduce the defect)."""
    return sorted(values)[int(len(values) * pct)]


def test_fast_p95_no_longer_equals_the_sample_maximum():
    # Nine fast samples plus one slow outlier. The old formula reported the
    # 2.5s outlier as p95 and failed the <=2s check on that sample alone.
    latencies = [0.1] * (FAST_SAMPLE_SIZE - 1) + [2.5]

    assert _old_percentile(latencies, 0.95) == 2.5  # reproduces the defect
    p95 = _percentile(latencies, 0.95)
    assert p95 < max(latencies)
    assert p95 == pytest.approx(1.42)  # 0.1 + (2.5 - 0.1) * 0.55
    assert p95 <= 2.0  # this check would have FAILED before the fix


def test_deep_p95_no_longer_equals_the_sample_maximum():
    # Four deep samples plus one slow outlier; n=5 means the old index (4)
    # was the maximum. Proper p95 must interpolate between ranks 3 and 4.
    latencies = [1.0] * (DEEP_SAMPLE_SIZE - 1) + [7.0]

    assert _old_percentile(latencies, 0.95) == 7.0  # reproduces the defect
    p95 = _percentile(latencies, 0.95)
    assert p95 < max(latencies)
    assert p95 == pytest.approx(5.8)  # 1.0 + (7.0 - 1.0) * 0.8
    assert p95 <= 6.0  # this check would have FAILED before the fix


def test_fast_p99_interpolates_instead_of_taking_the_maximum():
    latencies = [0.1] * (FAST_SAMPLE_SIZE - 1) + [2.5]

    assert _old_percentile(latencies, 0.99) == 2.5
    p99 = _percentile(latencies, 0.99)
    assert p99 < max(latencies)
    assert p99 == pytest.approx(2.284)  # 0.1 + (2.5 - 0.1) * 0.91


def test_percentile_uses_linear_interpolation():
    data = list(range(10))
    assert _percentile(data, 0.0) == 0
    assert _percentile(data, 0.5) == pytest.approx(4.5)
    assert _percentile(data, 0.95) == pytest.approx(8.55)
    assert _percentile(data, 0.99) == pytest.approx(8.91)
    assert _percentile(data, 1.0) == 9


def test_percentile_accepts_unsorted_input():
    assert _percentile([9, 1, 5, 3], 0.5) == pytest.approx(4.0)


def test_percentile_single_value():
    assert _percentile([3.5], 0.95) == 3.5


def test_percentile_empty_raises():
    with pytest.raises(ValueError):
        _percentile([], 0.95)
