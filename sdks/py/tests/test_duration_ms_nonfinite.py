from __future__ import annotations

import pytest

from loza.internal.jsonenc.number import duration_ms as internal_duration_ms
from loza.jsonenc.number import duration_ms


@pytest.mark.parametrize("impl", [duration_ms, internal_duration_ms])
@pytest.mark.parametrize("seconds", [float("nan"), float("inf"), float("-inf")])
def test_duration_ms_returns_zero_for_non_finite_input(impl, seconds: float) -> None:
    assert impl(seconds) == 0


@pytest.mark.parametrize("impl", [duration_ms, internal_duration_ms])
@pytest.mark.parametrize(
    ("seconds", "expected"),
    [(1.5, 1500), (0.0, 0), (-1.5, 0), (0.0009, 0), (2, 2000)],
)
def test_duration_ms_finite_input_unchanged(impl, seconds: float, expected: int) -> None:
    assert impl(seconds) == expected
