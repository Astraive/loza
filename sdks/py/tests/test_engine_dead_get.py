"""Regression test for issue #380 (MASS-0681).

``Engine._get`` was a private, never-called helper. Unlike ``_post`` it had no
error wrapping for ``HTTPError``/``URLError``, so its failures would have
propagated differently had it ever been used. It has been removed; this test
keeps the dead code (and its inconsistent error handling) from reappearing.
"""

from loza.cortex.engine import Engine


def test_engine_has_no_unused_get_helper() -> None:
    """The vestigial ``_get`` helper must stay removed from ``Engine``."""
    assert not hasattr(Engine, "_get"), (
        "Engine._get was removed as dead code (see #380); do not reintroduce it"
    )
