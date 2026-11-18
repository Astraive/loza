"""Regression tests for MASS-0720: one source of truth for redaction keys."""

from loza.core import redactor as core_redactor
from loza.redaction import DEFAULT_KEYS as PACKAGE_DEFAULT_KEYS
from loza.redaction import keys as keys_module


def test_keys_reexports_canonical_default_keys_by_identity():
    """keys.DEFAULT_KEYS must be the very object default_redactor() uses."""
    assert keys_module.DEFAULT_KEYS is core_redactor.DEFAULT_KEYS


def test_package_exports_canonical_default_keys_by_identity():
    """The public loza.redaction export must not be a second copy."""
    assert PACKAGE_DEFAULT_KEYS is core_redactor.DEFAULT_KEYS


def test_keys_see_canonical_updates():
    """A key added to the canonical set is visible via every re-export (no drift)."""
    sentinel = "mass_0720_drift_canary"
    assert sentinel not in core_redactor.DEFAULT_KEYS
    core_redactor.DEFAULT_KEYS.add(sentinel)
    try:
        assert sentinel in keys_module.DEFAULT_KEYS
        assert sentinel in PACKAGE_DEFAULT_KEYS
    finally:
        core_redactor.DEFAULT_KEYS.discard(sentinel)
