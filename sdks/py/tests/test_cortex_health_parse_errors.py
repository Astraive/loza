from __future__ import annotations

import pytest

from loza.cortex.client import CortexClient


class _BytesResponse:
    """Minimal urlopen response supporting context management and bounded reads."""

    def __init__(self, payload: bytes) -> None:
        self.payload = payload

    def __enter__(self) -> _BytesResponse:
        return self

    def __exit__(self, *_args: object) -> None:
        return None

    def read(self, size: int = -1) -> bytes:
        if size < 0:
            return self.payload
        return self.payload[:size]


def _stub_urlopen(monkeypatch: pytest.MonkeyPatch, payload: bytes) -> None:
    monkeypatch.setattr(
        "loza.cortex.client.urllib.request.urlopen",
        lambda *_args, **_kwargs: _BytesResponse(payload),
    )


def test_health_and_ready_accept_valid_json(monkeypatch: pytest.MonkeyPatch) -> None:
    _stub_urlopen(monkeypatch, b'{"status":"ok","ready":true}')
    client = CortexClient("https://cortex")
    assert client.health() is True
    assert client.ready() is True



@pytest.mark.parametrize("payload", [b"[]", b"null", b"42", b'"ok"'])
def test_health_and_ready_return_false_for_non_object_json(
    monkeypatch: pytest.MonkeyPatch,
    payload: bytes,
) -> None:
    _stub_urlopen(monkeypatch, payload)
    client = CortexClient("https://cortex")
    assert client.health() is False
    assert client.ready() is False

def test_health_returns_false_on_non_json_body(monkeypatch: pytest.MonkeyPatch) -> None:
    _stub_urlopen(monkeypatch, b"not json")
    assert CortexClient("https://cortex").health() is False


def test_ready_returns_false_on_non_json_body(monkeypatch: pytest.MonkeyPatch) -> None:
    _stub_urlopen(monkeypatch, b"not json")
    assert CortexClient("https://cortex").ready() is False


def test_health_returns_false_on_oversized_body(monkeypatch: pytest.MonkeyPatch) -> None:
    _stub_urlopen(monkeypatch, b'{"status":"ok"}')
    client = CortexClient("https://cortex", max_response_bytes=8)
    assert client.health() is False


def test_ready_returns_false_on_oversized_body(monkeypatch: pytest.MonkeyPatch) -> None:
    _stub_urlopen(monkeypatch, b'{"ready":true}')
    client = CortexClient("https://cortex", max_response_bytes=8)
    assert client.ready() is False
