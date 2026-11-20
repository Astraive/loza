"""Compatibility import path for the canonical ``loza.core.logger.Logger``.

This module used to hold a second, diverged copy of the ``Logger``
implementation. The package never used it (``loza.core`` exports ``Logger``
from ``.logger`` and ``config.new_client`` instantiates ``.logger.Logger``),
so fixes made to the canonical implementation never took effect for callers
importing from ``loza.core.client``.

The legacy import path is preserved as a thin re-export so that there is a
single implementation of ``Logger`` and no public API is broken.
"""

from __future__ import annotations

from .duplicate_policy import apply_duplicate
from .logger import Logger

__all__ = ["Logger"]
