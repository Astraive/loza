"""Backward-compatible alias for :mod:`loza.core.redactor`.

The SDK has a single redaction API in ``loza.core.redactor`` (also exported at
the ``loza`` package root). This module is kept as a thin re-export shim so
existing ``loza.redaction`` imports keep working without maintaining a second
set of redaction definitions.
"""

from __future__ import annotations

from ..core.redactor import DEFAULT_KEYS, Redactor, noop_redactor

__all__ = ["DEFAULT_KEYS", "Redactor", "noop_redactor"]
