from __future__ import annotations

import pytest

from loza.internal.jsonenc.encoder import encode as internal_encode
from loza.jsonenc.encoder import encode

ENCODERS = [encode, internal_encode]
NON_FINITE = [float("nan"), float("inf"), float("-inf")]


@pytest.mark.parametrize("encoder", ENCODERS)
@pytest.mark.parametrize("non_finite", NON_FINITE)
def test_encode_rejects_non_finite_floats(encoder, non_finite) -> None:
    """Non-finite floats must not be emitted as invalid JSON tokens (NaN/Infinity)."""
    with pytest.raises(ValueError):
        encoder({"x": non_finite})


@pytest.mark.parametrize("encoder", ENCODERS)
def test_encode_rejects_top_level_non_finite_float(encoder) -> None:
    with pytest.raises(ValueError):
        encoder(float("nan"))


@pytest.mark.parametrize("encoder", ENCODERS)
def test_encode_rejects_nested_non_finite_floats(encoder) -> None:
    with pytest.raises(ValueError):
        encoder({"items": [1.0, float("inf")]})


@pytest.mark.parametrize("encoder", ENCODERS)
def test_encode_still_handles_finite_values(encoder) -> None:
    assert encoder({"x": 1.5, "y": 2}) == '{"x":1.5,"y":2}'
