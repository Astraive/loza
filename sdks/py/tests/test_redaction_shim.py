from __future__ import annotations

import importlib

from loza import redaction as shim
from loza.core import redactor as core


def test_redaction_exports_alias_the_canonical_core_objects() -> None:
    """The compatibility shim must not keep a second copy of the redaction API."""
    assert shim.DEFAULT_KEYS is core.DEFAULT_KEYS
    assert shim.Redactor is core.Redactor
    assert shim.noop_redactor is core.noop_redactor

    # Legacy submodule import paths stay valid but resolve to the same objects.
    assert importlib.import_module("loza.redaction.keys").DEFAULT_KEYS is core.DEFAULT_KEYS
    assert importlib.import_module("loza.redaction.rules").Redactor is core.Redactor
    assert importlib.import_module("loza.redaction.noop").noop_redactor is core.noop_redactor


def test_noop_redactor_is_an_identity_redactor() -> None:
    payload = {"password": "keep-me", "nested": [1, 2]}

    assert core.noop_redactor()(payload) is payload
    assert shim.noop_redactor()(payload) is payload


def test_canonical_redactor_and_empty_sensitive_set_still_work() -> None:
    assert core.default_redactor()({"password": "secret", "safe": "ok"}) == {
        "password": "[REDACTED]",
        "safe": "ok",
    }
    assert core.sensitive_attrs_redactor(set())({"password": "secret"}) == {"password": "secret"}
