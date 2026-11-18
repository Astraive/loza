"""Regression tests for the legacy ``loza.core.client`` import path.

``core.client`` used to contain a stale, diverged copy of ``Logger`` that
never received fixes applied to the canonical ``core.logger.Logger``. It must
now be a thin re-export so there is a single implementation and the legacy
import path behaves exactly like the canonical one.
"""

import json

import loza
from loza.core import client, logger


def test_core_client_reexports_canonical_logger() -> None:
    assert client.Logger is logger.Logger
    assert client.Logger is loza.Logger
    assert client.__all__ == ["Logger"]


def test_legacy_import_path_exposes_full_logger_api() -> None:
    from loza.core.client import Logger

    # These members only existed on the canonical implementation; the stale
    # copy used to silently lack all of them.
    for name in ("alias", "checkpoint", "get", "get_group", "drain", "drop", "cancel"):
        assert hasattr(Logger, name), name


def test_legacy_import_path_honors_sensitive_attr_handling() -> None:
    # The stale client.py ignored Attr.sensitive, leaking values whose keys were
    # not covered by the default key-name redactor.
    sink = loza.MemorySink()
    log = client.Logger(loza.Config.test("legacy-path").with_sink(sink))
    ctx = log.start_event(loza.Params(event="secret.check"))
    log.enrich(ctx, loza.Attr("customer_note", "hunter2", sensitive=True))
    log.finish(ctx, "success")

    encoded = log.emit(ctx)
    payload = json.loads(encoded)

    assert payload["attrs"]["customer_note"] == "[REDACTED]"
    assert "hunter2" not in encoded
