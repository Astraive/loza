from __future__ import annotations

import json

import pytest

from loza.internal.jsonenc.string import quote as internal_quote
from loza.jsonenc.string import quote
from loza.jsonenc.string import quote as public_quote


# --- Tests introduced on main (HEAD/ours) ---
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


# --- Tests introduced by this PR (theirs) ---
QUOTE_IMPLEMENTATIONS = [internal_quote, public_quote]

# Values that the old `unicode_escape` based implementation turned into
# invalid JSON: non-ASCII, astral, control chars and embedded double quotes.
REPRESENTATIVE_VALUES = [
    "plain",
    "",
    "é",
    "😀",
    "\x01",
    "\x00\x08\x0b\x0c\x0e\x1f",
    'a"b',
    "line\nbreak",
    "back\\slash",
    'quote " and é and 😀 and \x01',
]


@pytest.mark.parametrize("quote_fn", QUOTE_IMPLEMENTATIONS, ids=["internal", "public"])
@pytest.mark.parametrize("value", REPRESENTATIVE_VALUES, ids=repr)
def test_quote_returns_valid_json_string(quote_fn, value: str) -> None:
    output = quote_fn(value)

    assert isinstance(output, str)
    assert output.startswith('"') and output.endswith('"')
    assert json.loads(output) == value


@pytest.mark.parametrize("quote_fn", QUOTE_IMPLEMENTATIONS, ids=["internal", "public"])
def test_quote_result_is_safe_to_embed_in_json(quote_fn) -> None:
    value = 'a"b\r\n\x01é😀\\'
    document = '{"value": ' + quote_fn(value) + "}"

    assert json.loads(document)["value"] == value


@pytest.mark.parametrize("quote_fn", QUOTE_IMPLEMENTATIONS, ids=["internal", "public"])
def test_quote_does_not_emit_python_specific_escapes(quote_fn) -> None:
    # Regression: unicode_escape emitted \xNN / \UNNNNNNNN and left the value
    # unwrapped, none of which is legal JSON.
    for value in ("é", "😀", "\x01"):
        output = quote_fn(value)
        assert "\\x" not in output
        assert "\\U" not in output
