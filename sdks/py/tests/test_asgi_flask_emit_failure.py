from __future__ import annotations

import asyncio
import importlib

import loza

asgi_module = importlib.import_module("loza.middleware.asgi.middleware")
flask_module = importlib.import_module("loza.middleware.flask.middleware")


class _DiagnosticFailure(BaseException):
    pass


def _fail_emit(_ctx):
    raise _DiagnosticFailure("telemetry failed")


def test_asgi_emit_failure_does_not_mask_application_error(monkeypatch):
    monkeypatch.setattr(asgi_module, "Emit", _fail_emit)
    loza.Configure(loza.Test("checkout").with_sink(loza.MemorySink()))

    async def app(_scope, _receive, _send):
        raise RuntimeError("original application failure")

    async def noop(*_args):
        return None

    middleware = asgi_module.Middleware(app, service="checkout")
    with __import__("pytest").raises(RuntimeError, match="original application failure"):
        asyncio.run(middleware({"type": "http", "method": "GET", "path": "/boom", "headers": []}, noop, noop))


def test_flask_emit_failure_does_not_mask_app_error(monkeypatch):
    monkeypatch.setattr(flask_module, "Emit", _fail_emit)
    loza.Configure(loza.Test("checkout").with_sink(loza.MemorySink()))

    def app(_environ, _start_response):
        raise RuntimeError("original application failure")

    middleware = flask_module.Middleware(app, service="checkout")
    with __import__("pytest").raises(RuntimeError, match="original application failure"):
        middleware({"REQUEST_METHOD": "GET", "PATH_INFO": "/boom"}, lambda *_args: None)


def test_flask_stream_emit_failure_does_not_replace_response_failure(monkeypatch):
    monkeypatch.setattr(flask_module, "Emit", _fail_emit)
    loza.Configure(loza.Test("checkout").with_sink(loza.MemorySink()))

    def app(_environ, start_response):
        start_response("200 OK", [])

        def body():
            yield b"part"
            raise RuntimeError("original stream failure")

        return body()

    middleware = flask_module.Middleware(app, service="checkout")
    iterator = iter(middleware({"REQUEST_METHOD": "GET", "PATH_INFO": "/boom"}, lambda *_args: None))
    assert next(iterator) == b"part"
    with __import__("pytest").raises(RuntimeError, match="original stream failure"):
        next(iterator)
