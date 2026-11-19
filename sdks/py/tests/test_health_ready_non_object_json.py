from __future__ import annotations

import pytest

from loza.core import http_client


class _Response:
    def __init__(self, payload: bytes) -> None:
        self.payload = payload

    def __enter__(self) -> _Response:
        return self

    def __exit__(self, *_args: object) -> None:
        return None

    def read(self, *_args: object) -> bytes:
        return self.payload


def _client() -> http_client.CollectorClient:
    return http_client.CollectorClient("https://collector/events", api_key="key", retries=0)


@pytest.mark.parametrize(
    "payload",
    [
        b"[]",
        b"[1, 2, 3]",
        b'"ok"',
        b"42",
        b"true",
        b"null",
    ],
)
def test_health_ready_treat_non_object_json_as_unhealthy(monkeypatch: pytest.MonkeyPatch, payload: bytes) -> None:
    """A valid JSON value that is not an object must not crash health()/ready()."""
    client = _client()
    monkeypatch.setattr(http_client, "urlopen", lambda *_args, **_kwargs: _Response(payload))

    assert client.health() is False
    assert client.ready() is False


def test_health_ready_accept_object_json(monkeypatch: pytest.MonkeyPatch) -> None:
    """Object JSON with an ok status still reports healthy/ready."""
    client = _client()
    monkeypatch.setattr(
        http_client,
        "urlopen",
        lambda *_args, **_kwargs: _Response(b'{"status":"ok","ready":true}'),
    )

    assert client.health() is True
    assert client.ready() is True


def test_health_ready_continue_after_non_object_json(monkeypatch: pytest.MonkeyPatch) -> None:
    """A non-object response on the first path falls through to the next path."""
    client = _client()
    responses = iter([_Response(b"[]"), _Response(b'{"status":"ok"}')])
    monkeypatch.setattr(http_client, "urlopen", lambda *_args, **_kwargs: next(responses))

    assert client.health() is True
