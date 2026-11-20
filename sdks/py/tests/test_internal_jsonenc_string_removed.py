from __future__ import annotations

import importlib
import importlib.util


CANONICAL_MODULE = "loza.jsonenc.string"
COMPAT_MODULE = "loza.internal.jsonenc.string"


def test_internal_jsonenc_string_module_remains_importable() -> None:
    """The internal ``jsonenc.string`` module is still a supported import path."""
    assert importlib.util.find_spec(COMPAT_MODULE) is not None
    assert importlib.import_module(COMPAT_MODULE) is importlib.import_module(COMPAT_MODULE)


def test_canonical_jsonenc_string_helpers_still_work() -> None:
    """The canonical ``jsonenc.string`` keeps the four helpers alive."""
    string = importlib.import_module(CANONICAL_MODULE)

    # quote returns a complete JSON string literal, including the surrounding quotes.
    assert string.quote("a\nb") == '"a\\nb"'
    assert string.scrub_control_chars("a\x00b\x0bc") == "abc"

    assert string.truncate_utf8("abc", 5) == "abc"
    assert string.truncate_utf8("abc", 2) == "ab"
    assert string.truncate_utf8("aé", 2) == "a"

    assert string.join_path(["a", "", "b"]) == "a.b"
