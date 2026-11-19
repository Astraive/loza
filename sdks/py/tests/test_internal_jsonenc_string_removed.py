from __future__ import annotations

import importlib
import importlib.util

import pytest


CANONICAL_MODULE = "loza.jsonenc.string"
DEAD_MODULE = "loza.internal.jsonenc.string"


def test_dead_internal_jsonenc_string_module_is_removed() -> None:
    """The dead, byte-identical duplicate of ``jsonenc.string`` must not exist."""
    assert importlib.util.find_spec(DEAD_MODULE) is None
    with pytest.raises(ModuleNotFoundError):
        importlib.import_module(DEAD_MODULE)


def test_canonical_jsonenc_string_helpers_still_work() -> None:
    """The single canonical ``jsonenc.string`` keeps the four helpers alive."""
    string = importlib.import_module(CANONICAL_MODULE)

    assert string.quote("a\nb") == "a\\nb"
    assert string.scrub_control_chars("a\x00b\x0bc") == "abc"

    assert string.truncate_utf8("abc", 5) == "abc"
    assert string.truncate_utf8("abc", 2) == "ab"
    assert string.truncate_utf8("aé", 2) == "a"

    assert string.join_path(["a", "", "b"]) == "a.b"
