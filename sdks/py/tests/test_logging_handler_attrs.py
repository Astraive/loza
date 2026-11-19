"""
Regression tests for the Python logging bridge's attribute mapping.

The ``log.logger``, ``log.module``, ``log.function`` and ``log.line`` names are
reserved for canonical LogRecord metadata. A user extra whose key collides with
one of them (``logger``, ``function``, ``line``) must not be mapped into the
``log.*`` namespace and overwritten by the canonical field.
"""

from __future__ import annotations

import logging

from loza.integrations.logging.handler import LozaHandler


def _record(**extra):
    record = logging.LogRecord(
        name="real.logger",
        level=logging.INFO,
        pathname="/srv/app/worker.py",
        lineno=77,
        msg="hello",
        args=(),
        exc_info=None,
        func="real_function",
    )
    # ``Logger.makeRecord`` merges extras into ``record.__dict__`` verbatim.
    record.__dict__.update(extra)
    return record


def test_canonical_log_fields_take_precedence_over_colliding_extras() -> None:
    record = _record(
        logger="user_logger",
        function="user_function",
        line=999,
        request_id="req-123",
    )

    attrs = LozaHandler()._attrs(record)

    # Canonical metadata comes from the LogRecord, never the colliding extras.
    assert attrs["log.logger"] == "real.logger"
    assert attrs["log.module"] == "worker"
    assert attrs["log.function"] == "real_function"
    assert attrs["log.line"] == 77

    # The colliding extra values must not appear anywhere in the mapping.
    assert "user_logger" not in attrs.values()
    assert "user_function" not in attrs.values()
    assert 999 not in attrs.values()

    # Unrelated extras are still mapped under ``log.*``.
    assert attrs["log.request_id"] == "req-123"


def test_extra_canonical_names_are_not_mapped_from_extra() -> None:
    """Colliding extras are skipped outright, not emitted under a different name."""
    attrs = LozaHandler()._attrs(_record(logger="user_logger", function="user_function", line=999))

    assert set(attrs) == {"log.logger", "log.module", "log.function", "log.line"}
