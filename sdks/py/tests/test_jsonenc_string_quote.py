from __future__ import annotations

import json

import pytest

from loza.internal.jsonenc.string import quote as internal_quote
from loza.jsonenc.string import quote


@pytest.mark.parametrize("quote_fn", [quote, internal_quote], ids=["public", "internal"])
@pytest.mark.parametrize(
    ("value", "expected"),
    [
        ("a\x00b", '"a\\u0000b"'),
        ("é", '"\\u00e9"'),
        ("😀", '"\\ud83d\\ude00"'),
        ('a"b', '"a\\"b"'),
        ("line\nbreak", '"line\\nbreak"'),
        ("back\\slash", '"back\\\\slash"'),
        ("plain", '"plain"'),
    ],
)
def test_quote_produces_valid_json_string_literal(quote_fn, value, expected):
    result = quote_fn(value)
    assert result == expected
    assert json.loads(result) == value


@pytest.mark.parametrize("quote_fn", [quote, internal_quote], ids=["public", "internal"])
def test_quote_output_is_ascii_only(quote_fn):
    # Preserve the previous contract that quoted output is safe to emit as ASCII.
    assert quote_fn("é 😀").isascii()
