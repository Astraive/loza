from __future__ import annotations

import io
from urllib.error import HTTPError

import pytest

from loza.core import lql


class _Response:
    """Minimal stand-in for the object returned by urllib.request.urlopen."""

    def __init__(self, payload: bytes, status: int = 200) -> None:
        self.payload = payload
        self.status = status

    def __enter__(self) -> _Response:
        return self

    def __exit__(self, *_args: object) -> None:
        return None

    def read(self, *_args: object) -> bytes:
        return self.payload


@pytest.mark.parametrize(
    "payload",
    [
        b"[1, 2, 3]",  # JSON array instead of an object
        b"null",  # JSON null instead of an object
        b'"just a string"',  # JSON scalar instead of an object
        b'{"columns": null, "rows": []}',  # non-iterable columns
        b'{"columns": 5, "rows": []}',  # non-iterable columns
        b'{"columns": [1, 2], "rows": []}',  # non-str/non-dict column entry
        b'{"columns": [{"name": 5}], "rows": []}',  # non-str column name
        b'{"columns": [], "rows": null}',  # non-list rows
        b'{"columns": [], "rows": {}}',  # wrong-type rows
    ],
)
def test_query_lql_malformed_response_raises_structured_error(
    monkeypatch: pytest.MonkeyPatch, payload: bytes
) -> None:
    monkeypatch.setattr(lql.urllib.request, "urlopen", lambda *_args, **_kwargs: _Response(payload))
    with pytest.raises(lql.LQLCompilationError, match="invalid result envelope"):
        lql.query_lql("https://collector", "select")


@pytest.mark.parametrize(
    "payload",
    [
        b'{"columns": [], "rows": [], "duration_ms": "abc"}',  # non-int duration
        b'{"columns": [], "rows": [], "row_count": "abc"}',  # non-int row count
    ],
)
def test_query_lql_invalid_metadata_uses_safe_fallback(
    monkeypatch: pytest.MonkeyPatch, payload: bytes
) -> None:
    """Non-numeric metadata is tolerated: main's _coerce_int safe fallback applies."""
    monkeypatch.setattr(lql.urllib.request, "urlopen", lambda *_args, **_kwargs: _Response(payload))
    result = lql.query_lql("https://collector", "select")
    assert result.duration_ms == 0
    assert result.row_count == 0


def test_query_lql_valid_response_still_decodes(monkeypatch: pytest.MonkeyPatch) -> None:
    payload = b'{"columns": [{"name": "id"}, "name"], "rows": [{"id": 1}], "row_count": 1, "duration_ms": 2}'
    monkeypatch.setattr(lql.urllib.request, "urlopen", lambda *_args, **_kwargs: _Response(payload))
    result = lql.query_lql("https://collector", "select")
    assert result.columns == ["id", "name"]
    assert result.rows == [{"id": 1}]
    assert result.row_count == 1 and result.duration_ms == 2


def test_query_lql_error_with_non_object_body_raises_structured_error(monkeypatch: pytest.MonkeyPatch) -> None:
    error = HTTPError("https://collector", 500, "bad", {}, io.BytesIO(b"[1, 2, 3]"))
    monkeypatch.setattr(lql.urllib.request, "urlopen", lambda *_args, **_kwargs: (_ for _ in ()).throw(error))
    with pytest.raises(lql.LQLCompilationError, match="HTTP 500") as exc:
        lql.query_lql("https://collector", "select")
    assert exc.value.status == 500


def test_query_sql_malformed_response_raises_structured_error(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(lql.urllib.request, "urlopen", lambda *_args, **_kwargs: _Response(b"null"))
    with pytest.raises(lql.LQLCompilationError, match="invalid result envelope"):
        lql.query_sql("https://collector", "select 1")
