from __future__ import annotations

import json

import pytest

from loza.internal.jsonenc.encoder import encode as internal_encode
from loza.jsonenc.encoder import encode as public_encode


@pytest.mark.parametrize("encode", [internal_encode, public_encode], ids=["internal", "public"])
def test_encode_finite_values_still_produces_valid_json(encode) -> None:
    result = encode({"x": 1.5, "y": [1, 2, 3]})
    assert json.loads(result) == {"x": 1.5, "y": [1, 2, 3]}
    assert "NaN" not in result and "Infinity" not in result


@pytest.mark.parametrize("encode", [internal_encode, public_encode], ids=["internal", "public"])
@pytest.mark.parametrize("non_finite", [float("nan"), float("inf"), float("-inf")], ids=["nan", "inf", "-inf"])
def test_encode_rejects_non_finite_floats(encode, non_finite) -> None:
    with pytest.raises(ValueError):
        encode({"x": non_finite})


@pytest.mark.parametrize("encode", [internal_encode, public_encode], ids=["internal", "public"])
def test_encode_rejects_nested_non_finite_floats(encode) -> None:
    with pytest.raises(ValueError):
        encode({"payload": {"values": [1.0, float("nan"), 3.0]}})


@pytest.mark.parametrize("encode", [internal_encode, public_encode], ids=["internal", "public"])
def test_encode_output_is_strict_rfc8259_json(encode) -> None:
    # A strict parser rejects NaN/Infinity tokens that json.dumps emits by default.
    def reject_constant(token: str):
        raise ValueError(f"invalid JSON constant: {token}")

    result = encode({"ok": True})
    assert json.loads(result, parse_constant=reject_constant) == {"ok": True}
from loza.jsonenc.encoder import encode


@pytest.mark.parametrize("encoder", [encode, internal_encode], ids=["public", "internal"])
def test_encode_mixed_key_types_does_not_raise(encoder) -> None:
    value = {1: "a", "b": "c"}

    # The reported failure: sort_keys=True raised
    # "TypeError: '<' not supported between instances of 'str' and 'int'".
    result = encoder(value)

    # Non-string keys are stringified just like the default json.dumps, and
    # the result is emitted in a deterministic, sorted order.
    assert result == json.dumps({"1": "a", "b": "c"}, separators=(",", ":"))
    assert json.loads(result) == json.loads(json.dumps(value))


@pytest.mark.parametrize("encoder", [encode, internal_encode], ids=["public", "internal"])
@pytest.mark.parametrize(
    "value, expected",
    [
        ({None: "a", "b": "c"}, '{"b":"c","null":"a"}'),
        ({True: "a", False: "b", "c": "nested"}, '{"c":"nested","false":"b","true":"a"}'),
        ({"outer": {2: "x", "a": "y"}}, '{"outer":{"2":"x","a":"y"}}'),
    ],
)
def test_encode_normalizes_and_sorts_mixed_keys(encoder, value, expected) -> None:
    assert encoder(value) == expected


@pytest.mark.parametrize("encoder", [encode, internal_encode], ids=["public", "internal"])
def test_encode_preserves_sorting_for_homogeneous_keys(encoder) -> None:
    assert encoder({"b": "c", "a": "d"}) == '{"a":"d","b":"c"}'
    assert encoder({10: "a", 2: "b"}) == '{"2":"b","10":"a"}'


@pytest.mark.parametrize("encoder", [encode, internal_encode], ids=["public", "internal"])
def test_encode_keeps_colliding_keys_without_data_loss(encoder) -> None:
    # int 1 and str "1" both stringify to "1"; default json.dumps emits both.
    assert encoder({1: "a", "1": "b"}) == json.dumps({1: "a", "1": "b"}, separators=(",", ":"))
