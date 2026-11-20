from decimal import Decimal

import pytest

from loza.internal.jsonenc import number as internal_number
from loza.internal.jsonenc.number import duration_ms as internal_duration_ms
from loza.jsonenc import number as number
from loza.jsonenc.number import duration_ms as public_duration_ms

MODULES = [number, internal_number]


@pytest.mark.parametrize("module", MODULES, ids=["public", "internal"])
def test_safe_number_rejects_huge_finite_decimal(module):
    """A finite Decimal too large for a float must not become inf (issue #829)."""
    value = Decimal("1e10000")

    assert value.is_finite()
    assert module.safe_number(value) is None


@pytest.mark.parametrize("module", MODULES, ids=["public", "internal"])
def test_safe_number_rejects_huge_negative_finite_decimal(module):
    assert module.safe_number(Decimal("-1e10000")) is None


@pytest.mark.parametrize("module", MODULES, ids=["public", "internal"])
def test_safe_number_keeps_representable_finite_decimal(module):
    assert module.safe_number(Decimal("1e10")) == 10_000_000_000.0


@pytest.mark.parametrize("module", MODULES, ids=["public", "internal"])
def test_safe_number_still_rejects_non_finite_decimal(module):
    assert module.safe_number(Decimal("NaN")) is None
    assert module.safe_number(Decimal("Infinity")) is None


@pytest.mark.parametrize("duration_ms", [internal_duration_ms, public_duration_ms])
def test_duration_ms_rounds_float_undershoot(duration_ms) -> None:
    # 1.001 * 1000 == 1000.9999999999999 in binary floating point; naive
    # truncation returned 1000 instead of 1001.
    assert duration_ms(1.001) == 1001
    assert duration_ms(4.02) == 4020
    assert duration_ms(0.001) == 1


@pytest.mark.parametrize("duration_ms", [internal_duration_ms, public_duration_ms])
def test_duration_ms_non_finite_inputs(duration_ms) -> None:
    # NaN/inf previously raised ValueError/OverflowError.
    assert duration_ms(float("nan")) == 0
    assert duration_ms(float("inf")) == 0
    assert duration_ms(float("-inf")) == 0


@pytest.mark.parametrize("duration_ms", [internal_duration_ms, public_duration_ms])
def test_duration_ms_clamps_negatives_and_zero(duration_ms) -> None:
    assert duration_ms(0.0) == 0
    assert duration_ms(-1.0) == 0
    assert duration_ms(1.5) == 1500
