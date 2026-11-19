"""Regression tests for query_sql response hardening (parity with query_lql)."""

from __future__ import annotations

import io
import json
from urllib.error import HTTPError

import pytest

from loza.core import lql


class _Response:
    def __init__(self, payload: bytes, status: int = 200) -> None:
        self.payload = payload
        self.status = status

    def __enter__(self) -> _Response:
        return self

    def __exit__(self, *_args: object) -> None:
        return None

    def read(self, *_args: object) -> bytes:
        return self.payload


def _raise(error: BaseException):
    def urlopen(*_args: object, **_kwargs: object):
        raise error

    return urlopen


def test_query_sql_caps_response_size(monkeypatch: pytest.MonkeyPatch) -> None:
    oversized = b"x" * (8 * 1024 * 1024 + 1)
    monkeypatch.setattr(lql.urllib.request, "urlopen", lambda *_a, **_k: _Response(oversized))
    with pytest.raises(lql.LQLCompilationError, match="exceeds the configured size limit"):
        lql.query_sql("https://collector", "select 1")


def test_query_sql_translates_http_errors(monkeypatch: pytest.MonkeyPatch) -> None:
    error = HTTPError(
        "https://collector", 422, "bad", {}, io.BytesIO(b'{"error":"bad sql","diagnostics":[{"line":2}]}')
    )
    monkeypatch.setattr(lql.urllib.request, "urlopen", _raise(error))
    with pytest.raises(lql.LQLCompilationError, match="bad sql") as exc:
        lql.query_sql("https://collector", "select 1")
    assert exc.value.status == 422 and exc.value.diagnostics


def test_query_sql_translates_transport_and_decode_errors(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(lql.urllib.request, "urlopen", _raise(OSError("down")))
    with pytest.raises(lql.LQLCompilationError, match="transport"):
        lql.query_sql("https://collector", "select 1")

    monkeypatch.setattr(lql.urllib.request, "urlopen", lambda *_a, **_k: _Response(b"not json"))
    with pytest.raises(lql.LQLCompilationError, match="decoding failed"):
        lql.query_sql("https://collector", "select 1")


def test_query_sql_validates_result_envelope(monkeypatch: pytest.MonkeyPatch) -> None:
    invalid = [
        b'{"columns": [], "rows": {}}',
        b'{"columns": {}, "rows": []}',
        b"[1, 2, 3]",
        b"null",
    ]
    for payload in invalid:
        monkeypatch.setattr(lql.urllib.request, "urlopen", lambda *_a, **_k: _Response(payload))
        with pytest.raises(lql.LQLCompilationError, match="invalid result envelope"):
            lql.query_sql("https://collector", "select 1")


def test_query_sql_success_still_returns_result(monkeypatch: pytest.MonkeyPatch) -> None:
    captured: list[object] = []

    def urlopen(req: object, timeout: float) -> _Response:
        captured.append((req, timeout))
        return _Response(json.dumps({"columns": ["id"], "rows": [{"id": 1}]}).encode())

    monkeypatch.setattr(lql.urllib.request, "urlopen", urlopen)
    result = lql.query_sql("https://collector", "select 1", api_key="sql-key")
    assert result.columns == ["id"] and result.rows == [{"id": 1}] and len(result) == 1
    req, timeout = captured[0]
    assert req.get_header("X-api-key") == "sql-key" and timeout == 30.0
