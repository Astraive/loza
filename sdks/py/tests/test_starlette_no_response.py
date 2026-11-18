from __future__ import annotations

import asyncio
import json

import loza
from loza.middleware.starlette import Middleware


async def _receive() -> dict:
    return {"type": "http.request", "body": b"", "more_body": False}


async def _run(app) -> dict:
    sink = loza.MemorySink()
    loza.Configure(loza.Test("checkout").with_sink(sink))
    middleware = Middleware(app, service="checkout")

    async def send(message: dict) -> None:
        pass

    await middleware(
        {
            "type": "http",
            "method": "GET",
            "path": "/silent",
            "headers": [],
            "client": ("127.0.0.1", 1234),
        },
        _receive,
        send,
    )

    assert len(sink.events) == 1
    return json.loads(sink.events[0])


def test_no_response_started_is_not_reported_as_a_500_error() -> None:
    """An app that returns without http.response.start has no status to report.

    Regression for MASS-0716: the old code defaulted ``status_code`` to 500, so
    this path emitted a false error event with no panic even though nothing
    failed.
    """

    async def silent_app(scope, receive, send):
        # Early-return without sending any response message.
        return

    payload = asyncio.run(_run(silent_app))

    assert payload["outcome"] == "unknown"
    assert "error" not in payload
    assert payload["event_state"] == "finished"


def test_observed_status_still_drives_outcome() -> None:
    async def success_app(scope, receive, send):
        await send({"type": "http.response.start", "status": 201, "headers": []})
        await send({"type": "http.response.body", "body": b"ok"})

    async def error_app(scope, receive, send):
        await send({"type": "http.response.start", "status": 503, "headers": []})
        await send({"type": "http.response.body", "body": b"nope"})

    assert asyncio.run(_run(success_app))["outcome"] == "success"
    assert asyncio.run(_run(error_app))["outcome"] == "error"
