from __future__ import annotations

import math
from decimal import Decimal

import pytest

from loza.internal.jsonenc.number import safe_number as internal_safe_number
from loza.jsonenc.number import safe_number as public_safe_number


@pytest.mark.parametrize("safe_number", [internal_safe_number, public_safe_number])
def test_finite_decimal_out_of_float_range_returns_none(safe_number) -> None:
    # Decimal('1e999') is finite, but float() overflows to inf.
    assert Decimal("1e999").is_finite()
    assert safe_number(Decimal("1e999")) is None
    assert safe_number(Decimal("-1e999")) is None


@pytest.mark.parametrize("safe_number", [internal_safe_number, public_safe_number])
def test_non_finite_decimal_returns_none(safe_number) -> None:
    assert safe_number(Decimal("NaN")) is None
    assert safe_number(Decimal("Infinity")) is None
    assert safe_number(Decimal("-Infinity")) is None


@pytest.mark.parametrize("safe_number", [internal_safe_number, public_safe_number])
def test_finite_decimal_in_range_returns_float(safe_number) -> None:
    result = safe_number(Decimal("1.5"))
    assert result == 1.5
    assert math.isfinite(result)


@pytest.mark.parametrize("safe_number", [internal_safe_number, public_safe_number])
def test_non_finite_float_returns_none(safe_number) -> None:
    assert safe_number(float("inf")) is None
    assert safe_number(float("-inf")) is None
    assert safe_number(float("nan")) is None
