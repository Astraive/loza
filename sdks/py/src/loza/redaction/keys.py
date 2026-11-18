"""Redaction key definitions - lightweight SDK stub.

Full redaction rules live in the loza-collector package. The canonical
safety-net key set is defined in :mod:`loza.core.redactor` and re-exported
here so there is a single source of truth.
"""

from __future__ import annotations

from loza.core.redactor import DEFAULT_KEYS

__all__ = ["DEFAULT_KEYS"]
