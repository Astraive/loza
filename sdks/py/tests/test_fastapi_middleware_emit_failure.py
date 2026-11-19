from __future__ import annotations

import asyncio
import importlib

import loza

fastapi_middleware = importlib.import_module("loza.middleware.fastapi.middleware")


class _AppError(RuntimeError):
    """Distinctive application error used to detect masking."""


async def _receive() -> dict:
    return {"type": "http.request", "body": b"", "more_body": False}


def _scope() -> dict:
    return {
        "type": "http",
        "method": "GET",
        "path": "/boom",
        "headers": [(b"user-agent", b"pytest")],
        "client": ("127.0.0.1", 1234),
    }


async def _send(_message: dict) -> None:
    return None


def _configure() -> None:
    loza.Configure(loza.Test("checkout").with_sink(loza.MemorySink()))


def _run(middleware: fastapi_middleware.Middleware) -> None:
    asyncio.run(middleware(_scope(), _receive, _send))


def test_emit_failure_does_not_mask_application_exception(monkeypatch) -> None:
    """An Emit failure in finally must not supersede a propagating app error."""
    calls = {"emit": 0}

    def _failing_emit(ctx) -> None:
        calls["emit"] += 1
        raise RuntimeError("telemetry delivery failed")

    monkeypatch.setattr(fastapi_middleware, "Emit", _failing_emit)

    async def app(scope, receive, send):
        await send({"type": "http.response.start", "status": 500, "headers": []})
        raise _AppError("original application failure")

    _configure()
    middleware = fastapi_middleware.Middleware(app, service="checkout")

    try:
        _run(middleware)
    except _AppError as exc:
        assert str(exc) == "original application failure"
    else:  # pragma: no cover
        raise AssertionError("application exception should propagate")

    assert calls["emit"] == 1


def test_emit_failure_does_not_turn_success_into_error(monkeypatch) -> None:
    """An Emit failure must not alter a successful request's outcome."""
    calls = {"emit": 0}

    def _failing_emit(ctx) -> None:
        calls["emit"] += 1
        raise RuntimeError("telemetry delivery failed")

    monkeypatch.setattr(fastapi_middleware, "Emit", _failing_emit)

    async def app(scope, receive, send):
        await send({"type": "http.response.start", "status": 200, "headers": []})
        await send({"type": "http.response.body", "body": b"ok"})

    _configure()
    middleware = fastapi_middleware.Middleware(app, service="checkout")

    _run(middleware)

    assert calls["emit"] == 1
