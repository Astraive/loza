"""Regression tests: upstream response bodies must not leak into exception messages."""

from __future__ import annotations

import io
import logging
from urllib.error import HTTPError

import pytest

from loza.cortex import engine as cortex_engine

_SECRET = b"token=SUPER_SECRET_UPSTREAM_VALUE at /internal/server/path"


def _raise_http_error(*_args: object, **_kwargs: object) -> None:
    raise HTTPError("https://cortex/reconstruct", 500, "Server Error", {}, io.BytesIO(_SECRET))


def test_post_error_message_omits_raw_body(monkeypatch: pytest.MonkeyPatch) -> None:
    """The exception message carries the status code but never the raw body."""
    monkeypatch.setattr(cortex_engine.urllib.request, "urlopen", _raise_http_error)

    engine = cortex_engine.Engine("https://cortex")
    with pytest.raises(RuntimeError) as exc_info:
        engine._post("/reconstruct", {"incident_id": "INC-1"})

    message = str(exc_info.value)
    assert "500" in message
    assert "SUPER_SECRET_UPSTREAM_VALUE" not in message
    assert "/internal/server/path" not in message
    # No repr of the raw bytes should appear either.
    assert "b'" not in message and 'b"' not in message


def test_post_error_body_only_at_debug_level(monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture) -> None:
    """A secret in the upstream body is only logged at DEBUG, never by default."""
    monkeypatch.setattr(cortex_engine.urllib.request, "urlopen", _raise_http_error)

    engine = cortex_engine.Engine("https://cortex")

    with caplog.at_level(logging.WARNING, logger="loza.cortex.engine"):
        with pytest.raises(RuntimeError):
            engine._post("/reconstruct", {})
    assert "SUPER_SECRET_UPSTREAM_VALUE" not in caplog.text

    caplog.clear()
    with caplog.at_level(logging.DEBUG, logger="loza.cortex.engine"):
        with pytest.raises(RuntimeError):
            engine._post("/reconstruct", {})
    assert "SUPER_SECRET_UPSTREAM_VALUE" in caplog.text
