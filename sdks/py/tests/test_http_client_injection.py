from __future__ import annotations

import json

import pytest

from loza.core import http_client as hc

_VALID_EVENT = json.dumps(
    {
        "timestamp": "2025-01-01T00:00:00Z",
        "schema_version": "v1",
        "event_version": "v1",
        "event_id": "e",
        "request_id": "r",
        "service": "s",
        "event": "x",
        "kind": "event",
    }
)


class _Response:
    """Minimal context-manager response usable as an opener result."""

    def __init__(self, body: bytes = b"", *, status: int = 200, lines: list[bytes] | None = None) -> None:
        self._body = body
        self.status = status
        self.headers: dict[str, str] = {}
        self._lines = lines

    def getcode(self) -> int:
        return self.status

    def read(self) -> bytes:
        return self._body

    def __enter__(self) -> _Response:
        return self

    def __exit__(self, *_exc: object) -> bool:
        return False

    def __iter__(self):
        return iter(self._lines if self._lines is not None else [self._body])


class _SpyClient:
    """Records every open() call so we can prove the hook is consulted."""

    def __init__(self, responder) -> None:
        self.calls: list[tuple[object, float]] = []
        self._responder = responder

    def open(self, request, timeout: float = 5.0):
        self.calls.append((request, timeout))
        return self._responder(request, timeout)


def _responder(request, timeout):
    url = request.full_url
    if url.endswith("/events"):
        return _Response(b'{"accepted":1,"rejected":0,"invalid":0}')
    if "/health" in url:
        return _Response(b'{"status":"ok"}')
    if "/ready" in url:
        return _Response(b'{"status":"ready"}')
    if url.endswith("/version"):
        return _Response(b'{"version":"v"}')
    if url.endswith("/status"):
        return _Response(b'{"state":"ok"}')
    if "/tail" in url:
        return _Response(b"", lines=[b'{"event":"one"}\n', b"not-json\n"])
    return _Response(b"{}")


def test_collector_client_routes_every_request_through_injected_client(monkeypatch: pytest.MonkeyPatch) -> None:
    def forbidden_urlopen(*_args, **_kwargs):
        raise AssertionError("module-level urlopen must not be used when an http client is injected")

    monkeypatch.setattr(hc, "urlopen", forbidden_urlopen)

    spy = _SpyClient(_responder)
    client = hc.CollectorClient("https://collector/events", api_key="key", retries=0, http_client=spy)

    assert client.send_batch([_VALID_EVENT]).accepted == 1
    assert client.health() is True
    assert client.ready() is True
    assert client.version() == {"version": "v"}
    assert client.status() == {"state": "ok"}
    assert list(client.tail_lines(service="s", kind="event")) == [{"event": "one"}, "not-json"]

    # The CollectorClient API extensions use _collector_request; they must
    # consult the same injected opener rather than the module-level urlopen.
    assert client.validate({"event": "e"}) == {}
    assert client.query(q="x") == {}
    assert client.tail(service="s") == {}
    assert client.replay(id="e") == {}
    assert client.dlq_list() == {}
    assert client.dlq_read("d") == {}
    assert client.delete(event_id="e") == {}

    # 6 core paths + 7 extension paths all went through the injected opener.
    assert len(spy.calls) == 13
    assert all(isinstance(request, hc.Request) for request, _ in spy.calls)


def test_wrapped_and_round_tripper_clients_are_honored() -> None:
    first = _SpyClient(_responder)
    second = _SpyClient(_responder)

    assert hc.WrapHTTPClient(first) is first
    assert hc.NewRoundTripper(second) is second

    client = hc.CollectorClient("https://collector/events", retries=0, http_client=hc.WrapHTTPClient(first))
    assert client.health() is True
    assert len(first.calls) == 1


def test_default_client_keeps_legacy_transport(monkeypatch: pytest.MonkeyPatch) -> None:
    """Backwards compatibility: without injection, requests use urlopen."""
    calls: list[object] = []

    def fake_urlopen(request, timeout=5.0):
        calls.append(request)
        return _Response(b'{"status":"ok"}')

    monkeypatch.setattr(hc, "urlopen", fake_urlopen)

    client = hc.CollectorClient("https://collector/events", retries=0)
    assert client.http_client is None
    assert client.health() is True
    assert len(calls) == 1


def test_default_send_batch_keeps_redirect_guard(monkeypatch: pytest.MonkeyPatch) -> None:
    """Injection must not weaken the existing same-origin redirect protection."""
    seen: list[tuple[object, float, str]] = []

    def fake_guard(request, timeout, endpoint):
        seen.append((request, timeout, endpoint))
        return _Response(b'{"accepted":1,"rejected":0,"invalid":0}')

    def forbidden_urlopen(*_args, **_kwargs):
        raise AssertionError("the guarded default batch path must not use bare urlopen")

    monkeypatch.setattr(hc, "_open_with_redirect_guard", fake_guard)
    monkeypatch.setattr(hc, "urlopen", forbidden_urlopen)

    client = hc.CollectorClient("https://collector/events", retries=0)
    assert client.send_batch([_VALID_EVENT]).accepted == 1
    assert len(seen) == 1
    assert seen[0][2] == "https://collector/events"
