from __future__ import annotations

import asyncio
import importlib
import json
from types import SimpleNamespace

import pytest

import loza
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
def test_asgi_middleware_emits_error_event_when_route_resolver_raises() -> None:
    """A failing resolver must not bypass the one-wide-event-per-request lifecycle."""

    async def run() -> None:
        app_called = False

        async def app(scope, receive, send):
            nonlocal app_called
            app_called = True

        def broken_resolver(scope):
            raise RuntimeError("route resolver failed")

        sink = loza.MemorySink()
        loza.Configure(loza.Test("checkout").with_sink(sink))
        middleware = Middleware(app, service="checkout", route_resolver=broken_resolver)

        async def send(message: dict) -> None:
            return None

        try:
            await middleware(
                {
                    "type": "http",
                    "method": "GET",
                    "path": "/boom",
                    "headers": [(b"user-agent", b"pytest")],
                    "client": ("127.0.0.1", 1234),
                },
                _receive,
                send,
            )
        except RuntimeError as exc:
            assert str(exc) == "route resolver failed"
        else:  # pragma: no cover
            raise AssertionError("resolver error should propagate to the caller")

        assert app_called is False
        assert len(sink.events) == 1
        payload = json.loads(sink.events[0])
        assert payload["kind"] == "http"
        assert payload["outcome"] == "error"
        assert payload["error"]["type"] == "RuntimeError"
        assert payload["error"]["message"] == "route resolver failed"
        assert payload["attrs"]["panic"] is True
        assert payload["http"]["path"] == "/boom"
        assert payload["http"]["route"] == "/boom"

    asyncio.run(run())


def test_asgi_middleware_records_ordinary_exception_without_panic_flag() -> None:
    async def run() -> None:
        async def app(scope, receive, send):
            await send({"type": "http.response.start", "status": "not-an-int", "headers": []})

        sink = loza.MemorySink()
        loza.Configure(loza.Test("checkout").with_sink(sink))
        middleware = Middleware(app, service="checkout")

        async def send(message: dict) -> None:
            return None

        try:
            await middleware(
                {
                    "type": "http",
                    "method": "GET",
                    "path": "/boom",
                    "headers": [],
                    "client": ("127.0.0.1", 1234),
                },
                _receive,
                send,
            )
        except ValueError as exc:
            assert "invalid literal for int()" in str(exc)
        else:  # pragma: no cover
            raise AssertionError("malformed ASGI status should raise ValueError")

        assert len(sink.events) == 1
        payload = json.loads(sink.events[0])
        assert payload["outcome"] == "error"
        assert payload["error"]["type"] == "ValueError"
        assert "panic" not in payload.get("attrs", {})

    asyncio.run(run())
def test_response_capture_accumulates_without_reset() -> None:
    from loza.middleware.asgi.response import ResponseCapture

    capture = ResponseCapture()
    assert capture.status_code == 500
    assert capture.bytes == 0

    capture.record(status_code=201)
    capture.record(body_bytes=2)
    capture.record(body_bytes=3)

    assert capture.status_code == 201
    assert capture.bytes == 5


def test_asgi_middleware_uses_response_capture() -> None:
    """ResponseCapture is wired into the middleware instead of being orphaned."""
    import importlib

    # importlib is needed because the `middleware` function re-exported by
    # loza.middleware.asgi shadows the submodule attribute of the same name.
    asgi_middleware = importlib.import_module("loza.middleware.asgi.middleware")

    captured: list[object] = []
    original = asgi_middleware.ResponseCapture

    class SpyCapture(original):
        def __init__(self, *args, **kwargs) -> None:
            super().__init__(*args, **kwargs)
            captured.append(self)

    asgi_middleware.ResponseCapture = SpyCapture
    try:
        asyncio.run(_run_app([]))
    finally:
        asgi_middleware.ResponseCapture = original

    assert len(captured) == 1
    assert captured[0].status_code == 201
    assert captured[0].bytes == 2


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


def _django_request(path: str, resolver_match=None):
    return SimpleNamespace(
        method="GET",
        path=path,
        META={},
        resolver_match=resolver_match,
    )


def test_django_middleware_records_matched_route_template() -> None:
    from loza.middleware.django import LozaMiddleware

    match = SimpleNamespace(route="users/<int:pk>/", view_name="user-detail")
    request = _django_request("/users/42/", match)

    sink = loza.MemorySink()
    loza.Configure(loza.Test("checkout").with_sink(sink))
    middleware = LozaMiddleware(lambda _req: SimpleNamespace(status_code=200), service="checkout")
    middleware(request)

    assert len(sink.events) == 1
    payload = json.loads(sink.events[0])
    assert payload["path"] == "/users/42/"
    assert payload["route"] == "users/<int:pk>/"
    assert payload["http"]["route"] == "users/<int:pk>/"


def test_django_middleware_prefers_route_over_view_name() -> None:
    from loza.middleware.django import LozaMiddleware

    match = SimpleNamespace(route="users/<int:pk>/", view_name="user-detail")
    request = _django_request("/users/42/", match)

    sink = loza.MemorySink()
    loza.Configure(loza.Test("checkout").with_sink(sink))
    middleware = LozaMiddleware(lambda _req: SimpleNamespace(status_code=200), service="checkout")
    middleware(request)

    payload = json.loads(sink.events[0])
    assert payload["route"] == "users/<int:pk>/"


def test_django_middleware_falls_back_to_view_name() -> None:
    from loza.middleware.django import LozaMiddleware

    match = SimpleNamespace(view_name="user-detail")
    request = _django_request("/users/42/", match)

    sink = loza.MemorySink()
    loza.Configure(loza.Test("checkout").with_sink(sink))
    middleware = LozaMiddleware(lambda _req: SimpleNamespace(status_code=200), service="checkout")
    middleware(request)

    payload = json.loads(sink.events[0])
    assert payload["route"] == "user-detail"


def test_django_middleware_falls_back_to_path_without_resolver_match() -> None:
    from loza.middleware.django import LozaMiddleware

    request = _django_request("/not-a-real-route/")

    sink = loza.MemorySink()
    loza.Configure(loza.Test("checkout").with_sink(sink))
    middleware = LozaMiddleware(lambda _req: SimpleNamespace(status_code=404), service="checkout")
    middleware(request)

    payload = json.loads(sink.events[0])
    assert payload["route"] == "/not-a-real-route/"


def test_django_middleware_uses_route_resolved_during_view_dispatch() -> None:
    # New-style Django middleware is entered before URL resolution, so
    # resolver_match does not exist until the view dispatch (get_response).
    from loza.middleware.django import LozaMiddleware

    request = _django_request("/users/7/")

    def get_response(_req):
        _req.resolver_match = SimpleNamespace(route="users/<int:pk>/", view_name="user-detail")
        return SimpleNamespace(status_code=200)

    sink = loza.MemorySink()
    loza.Configure(loza.Test("checkout").with_sink(sink))
    middleware = LozaMiddleware(get_response, service="checkout")
    middleware(request)

    payload = json.loads(sink.events[0])
    assert payload["path"] == "/users/7/"
    assert payload["route"] == "users/<int:pk>/"
def test_starlette_middleware_without_app_raises_configuration_error() -> None:
    """An unconfigured Starlette middleware must not silently violate the ASGI protocol."""
    from loza.middleware.starlette import Middleware as StarletteMiddleware
    from loza.middleware.starlette import middleware as starlette_middleware

    async def receive() -> dict:
        return {"type": "http.request", "body": b"", "more_body": False}

    scope = {
        "type": "http",
        "method": "GET",
        "path": "/health",
        "headers": [],
        "client": ("127.0.0.1", 1234),
    }

    # Both the class default and the factory default build an instance with app=None.
    for instance in (StarletteMiddleware(), starlette_middleware()):
        sent: list[dict] = []

        async def send(message: dict) -> None:
            sent.append(message)

        with pytest.raises(RuntimeError, match="no wrapped ASGI app"):
            asyncio.run(instance(scope, receive, send))
        assert sent == []
def test_flask_middleware_factory_accepts_extra_config() -> None:
    from loza.middleware.flask import middleware

    def app(_environ, start_response):
        start_response("200 OK", [])
        return [b"ok"]

    # The factory forwards **config to the constructor; arbitrary keys must be
    # accepted rather than raising "unexpected keyword argument" (issue #846).
    instance = middleware(app, service="checkout", debug=True, sample_rate=0.5)

    assert instance.app is app
    assert instance.service == "checkout"
    assert instance.config == {"debug": True, "sample_rate": 0.5}


def test_flask_middleware_factory_still_accepts_service_only() -> None:
    from loza.middleware.flask import middleware

    instance = middleware(service="checkout")

    assert instance.app is None
    assert instance.service == "checkout"
    assert instance.config == {}
def test_flask_middleware_malformed_status_defaults_to_500() -> None:
    from loza.middleware.flask import Middleware as FlaskMiddleware

    # A whitespace-only status makes split() return [], and a non-numeric status
    # makes int() fail. Neither may escape capture_start_response into the app.
    for malformed_status in ("   ", "not-a-number"):
        captured = []

        def app(_environ, start_response, _status=malformed_status):
            start_response(_status, [])
            return iter([b"ok"])

        def start_response(status, response_headers, exc_info=None):
            captured.append(status)

        sink = loza.MemorySink()
        loza.Configure(loza.Test("checkout").with_sink(sink))
        middleware = FlaskMiddleware(app, service="checkout")

        response = middleware(
            {"REQUEST_METHOD": "GET", "PATH_INFO": "/bad-status"},
            start_response,
        )

        assert list(response) == [b"ok"]
        assert captured == [malformed_status]

        assert len(sink.events) == 1
        payload = json.loads(sink.events[0])
        # status_code is a canonical field and is stripped from the payload;
        # an "error" outcome confirms the parse fell back to 500.
        assert payload["outcome"] == "error"
