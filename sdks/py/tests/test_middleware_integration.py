from __future__ import annotations

import asyncio
import importlib
import json

import loza
import pytest
from loza.middleware.asgi import Middleware


async def _receive() -> dict:
    return {"type": "http.request", "body": b"", "more_body": False}


async def _run_app(messages: list[dict], middleware_cls=None) -> None:
    async def app(scope, receive, send):
        await send({"type": "http.response.start", "status": 201, "headers": []})
        await send({"type": "http.response.body", "body": b"ok"})

    sink = loza.MemorySink()
    loza.Configure(loza.Test("checkout").with_sink(sink))
    middleware = (middleware_cls or Middleware)(app, service="checkout")

    async def send(message: dict) -> None:
        messages.append(message)

    await middleware(
        {
            "type": "http",
            "method": "GET",
            "path": "/health",
            "headers": [(b"user-agent", b"pytest")],
            "client": ("127.0.0.1", 1234),
        },
        _receive,
        send,
    )

    assert len(sink.events) == 1
    payload = json.loads(sink.events[0])
    assert payload["kind"] == "http"
    assert payload["http"]["user_agent"] == "pytest"
    assert payload["http"]["remote_ip"] == "127.0.0.1"
    # status_code is canonical and must be recorded, not silently dropped
    assert payload["status_code"] == 201
    assert payload["http"]["status_code"] == 201
    assert "status_code" not in payload.get("attrs", {})
    assert payload["attrs"]["response_bytes"] == 2


def test_asgi_middleware_captures_http_request() -> None:
    asyncio.run(_run_app([]))


def test_fastapi_middleware_records_status_code() -> None:
    from loza.middleware.fastapi import Middleware as FastAPIMiddleware

    asyncio.run(_run_app([], FastAPIMiddleware))


def test_starlette_middleware_records_status_code() -> None:
    from loza.middleware.starlette import Middleware as StarletteMiddleware

    asyncio.run(_run_app([], StarletteMiddleware))


def test_flask_middleware_streams_response_without_buffering() -> None:
    from loza.middleware.flask import Middleware as FlaskMiddleware

    produced = 0

    def app(_environ, start_response):
        start_response("200 OK", [])

        def body():
            nonlocal produced
            for chunk in (b"one", "two"):
                produced += 1
                yield chunk

        return body()

    sink = loza.MemorySink()
    loza.Configure(loza.Test("checkout").with_sink(sink))
    middleware = FlaskMiddleware(app, service="checkout")

    response = middleware({"REQUEST_METHOD": "GET", "PATH_INFO": "/stream"}, lambda *_args: None)

    assert produced == 0
    iterator = iter(response)
    assert next(iterator) == b"one"
    assert produced == 1
    assert next(iterator) == b"two"
    assert produced == 2

    try:
        next(iterator)
    except StopIteration:
        pass
    else:  # pragma: no cover
        raise AssertionError("response should be exhausted")

    assert len(sink.events) == 1
    payload = json.loads(sink.events[0])
    assert payload["status_code"] == 200
    assert payload["http"]["status_code"] == 200
    assert payload["attrs"]["response_bytes"] == 6


def test_flask_middleware_without_app_calls_start_response_and_emits_event() -> None:
    from loza.middleware.flask import Middleware as FlaskMiddleware

    sink = loza.MemorySink()
    loza.Configure(loza.Test("checkout").with_sink(sink))
    middleware = FlaskMiddleware(service="checkout")  # app intentionally omitted (the common miswire)

    captured: dict = {}

    def start_response(status, headers, exc_info=None):
        captured["status"] = status
        captured["headers"] = headers

    response = middleware({"REQUEST_METHOD": "GET", "PATH_INFO": "/broken"}, start_response)

    assert captured["status"].startswith("500")
    assert ("Content-Type", "text/plain") in captured["headers"]
    assert list(response) == [b"Internal Server Error"]

    assert len(sink.events) == 1
    payload = json.loads(sink.events[0])
    assert payload["outcome"] == "error"
    assert payload["error"]["message"] == "loza.flask.middleware: no WSGI app configured (app=None)"
    assert payload["attrs"]["panic"] is True


def test_flask_middleware_closes_original_iterable_on_close() -> None:
    from loza.middleware.flask import Middleware as FlaskMiddleware

    class StreamingBody:
        def __init__(self) -> None:
            self.closed = False

        def __iter__(self):
            yield b"first"
            yield b"second"

        def close(self) -> None:
            self.closed = True

    body = StreamingBody()

    def app(_environ, start_response):
        start_response("200 OK", [])
        return body

    sink = loza.MemorySink()
    loza.Configure(loza.Test("checkout").with_sink(sink))
    middleware = FlaskMiddleware(app, service="checkout")

    response = middleware({"REQUEST_METHOD": "GET", "PATH_INFO": "/stream"}, lambda *_args: None)
    iterator = iter(response)

    assert next(iterator) == b"first"
    iterator.close()

    assert body.closed is True
    assert len(sink.events) == 1
    payload = json.loads(sink.events[0])
    assert payload["status_code"] == 200
    assert payload["http"]["status_code"] == 200
    assert payload["attrs"]["response_bytes"] == 5


async def _run_starlette_app(app, messages):
    starlette_module = importlib.import_module("loza.middleware.starlette.middleware")
    sink = loza.MemorySink()
    loza.Configure(loza.Test("checkout").with_sink(sink))
    middleware = starlette_module.Middleware(app, service="checkout")

    async def receive():
        return {"type": "http.request", "body": b"", "more_body": False}

    async def send(message):
        messages.append(message)

    await middleware(
        {
            "type": "http",
            "method": "GET",
            "path": "/boom",
            "headers": [],
            "client": ("127.0.0.1", 1234),
        },
        receive,
        send,
    )


def test_starlette_emit_failure_does_not_mask_app_exception(monkeypatch) -> None:
    starlette_module = importlib.import_module("loza.middleware.starlette.middleware")

    def failing_emit(_ctx):
        raise loza.EventClosedError("telemetry unavailable")

    monkeypatch.setattr(starlette_module, "Emit", failing_emit)

    async def app(_scope, _receive, _send):
        raise RuntimeError("original application failure")

    with pytest.raises(RuntimeError, match="original application failure"):
        asyncio.run(_run_starlette_app(app, []))


def test_starlette_emit_base_exception_does_not_mask_app_exception(monkeypatch) -> None:
    starlette_module = importlib.import_module("loza.middleware.starlette.middleware")

    class SinkBaseFailure(BaseException):
        pass

    def failing_emit(_ctx):
        raise SinkBaseFailure("sink crashed")

    monkeypatch.setattr(starlette_module, "Emit", failing_emit)

    async def app(_scope, _receive, _send):
        raise RuntimeError("original application failure")

    with pytest.raises(RuntimeError, match="original application failure"):
        asyncio.run(_run_starlette_app(app, []))


def test_starlette_emit_failure_does_not_break_successful_request(monkeypatch) -> None:
    starlette_module = importlib.import_module("loza.middleware.starlette.middleware")

    def failing_emit(_ctx):
        raise loza.EventValidationError("telemetry validation failed")

    monkeypatch.setattr(starlette_module, "Emit", failing_emit)

    async def app(_scope, _receive, send):
        await send({"type": "http.response.start", "status": 200, "headers": []})
        await send({"type": "http.response.body", "body": b"ok"})

    messages = []
    asyncio.run(_run_starlette_app(app, messages))
    assert any(message.get("type") == "http.response.start" for message in messages)


def test_django_middleware_records_status_code() -> None:
    from loza.middleware.django import LozaMiddleware

    class Response:
        status_code = 202

    class Request:
        method = "GET"
        path = "/health"
        META = {"HTTP_USER_AGENT": "pytest", "REMOTE_ADDR": "127.0.0.1"}

    sink = loza.MemorySink()
    loza.Configure(loza.Test("checkout").with_sink(sink))
    middleware = LozaMiddleware(lambda _request: Response(), service="checkout")

    middleware(Request())

    assert len(sink.events) == 1
    payload = json.loads(sink.events[0])
    assert payload["status_code"] == 202
    assert payload["http"]["status_code"] == 202
    assert "status_code" not in payload.get("attrs", {})
