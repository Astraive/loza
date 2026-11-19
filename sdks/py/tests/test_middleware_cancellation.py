from __future__ import annotations

import asyncio
import json

import loza
from loza.middleware.asgi import Middleware as ASGIMiddleware
from loza.middleware.fastapi import Middleware as FastAPIMiddleware


async def _run_cancelled_request(middleware_cls) -> dict:
    """Drive an HTTP request whose app is cancelled and return the emitted payload."""
    sink = loza.MemorySink()
    loza.Configure(loza.Test("checkout").with_sink(sink))

    async def app(scope, receive, send):
        raise asyncio.CancelledError("client disconnected")

    middleware = middleware_cls(app, service="checkout")

    async def receive() -> dict:
        return {"type": "http.request", "body": b"", "more_body": False}

    async def send(message: dict) -> None:
        return None

    scope = {
        "type": "http",
        "method": "GET",
        "path": "/health",
        "headers": [(b"user-agent", b"pytest")],
        "client": ("127.0.0.1", 1234),
    }

    try:
        await middleware(scope, receive, send)
    except asyncio.CancelledError as exc:
        # Cancellation must still propagate to the ASGI server.
        assert str(exc) == "client disconnected"
    else:  # pragma: no cover
        raise AssertionError("CancelledError should propagate")

    assert len(sink.events) == 1
    return json.loads(sink.events[0])


def _assert_cancellation_finishes_event(middleware_cls) -> None:
    payload = asyncio.run(_run_cancelled_request(middleware_cls))
    assert payload["outcome"] == "error"
    assert payload["error"]["type"] == "CancelledError"
    assert payload["error"]["message"] == "client disconnected"
    assert payload["duration_ms"] >= 0


def test_fastapi_middleware_finishes_event_on_client_cancellation() -> None:
    _assert_cancellation_finishes_event(FastAPIMiddleware)


def test_asgi_middleware_finishes_event_on_client_cancellation() -> None:
    _assert_cancellation_finishes_event(ASGIMiddleware)
