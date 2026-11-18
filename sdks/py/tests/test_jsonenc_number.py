from decimal import Decimal

import pytest

from loza.internal.jsonenc import number as internal_number
from loza.jsonenc import number as number

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
