from __future__ import annotations

import pytest

import loza
from loza.core.schema import EventView, FlatSchema


def _flatten(value: dict[str, object]) -> dict[str, object]:
    out: dict[str, object] = {}
    FlatSchema()._flatten("", value, out)
    return out


def test_flat_schema_preserves_empty_dict_fields() -> None:
    flattened = _flatten({"empty": {}, "scalar": 1, "items": []})

    assert flattened == {"empty": {}, "scalar": 1, "items": []}


def test_flat_schema_raises_on_flattened_key_collision() -> None:
    with pytest.raises(ValueError, match="a_b"):
        _flatten({"a_b": 1, "a": {"b": 2}})


def test_flat_schema_detects_collision_with_empty_container() -> None:
    with pytest.raises(ValueError, match="x_a"):
        _flatten({"x_a": 1, "x": {"a": {}}})

    with pytest.raises(ValueError, match="x_a"):
        _flatten({"x": {"a": {}}, "x_a": 1})


def test_flat_schema_keeps_distinct_paths_and_rootless_empty_payload() -> None:
    assert _flatten({"a": {"b": 1}, "a_b_c": 2}) == {"a_b": 1, "a_b_c": 2}
    assert _flatten({}) == {}


def test_flat_schema_encode_preserves_empty_attr_group() -> None:
    ctx = loza.EventContext(service="checkout", params=loza.Params(event="test.event"))
    ctx.attrs["empty"] = {}

    encoded = FlatSchema().encode(EventView(ctx))

    assert encoded["attrs_empty"] == {}


def test_flat_schema_encode_raises_on_colliding_attr_paths() -> None:
    ctx = loza.EventContext(service="checkout", params=loza.Params(event="test.event"))
    ctx.attrs["user_id"] = "first"
    ctx.attrs["user"] = {"id": "second"}

    with pytest.raises(ValueError, match="attrs_user_id"):
        FlatSchema().encode(EventView(ctx))
