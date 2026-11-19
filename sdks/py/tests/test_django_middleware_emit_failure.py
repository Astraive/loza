from __future__ import annotations

import importlib

import loza
import pytest

middleware_module = importlib.import_module("loza.middleware.django.middleware")


class _Request:
    method = "GET"
    path = "/health"
    META = {"HTTP_USER_AGENT": "pytest", "REMOTE_ADDR": "127.0.0.1"}


class _Response:
    status_code = 200


def _configure(monkeypatch):
    """Configure an isolated in-memory logger and make Emit fail loudly."""
    loza.Configure(loza.Test("checkout").with_sink(loza.MemorySink()))

    def boom(_ctx):
        raise RuntimeError("emit backend down")

    monkeypatch.setattr(middleware_module, "Emit", boom)


def test_emit_failure_does_not_replace_successful_response(monkeypatch):
    _configure(monkeypatch)
    mw = middleware_module.LozaMiddleware(lambda _req: _Response(), service="checkout")

    response = mw(_Request())

    assert response is not None
    assert response.status_code == 200


def test_emit_failure_does_not_mask_handler_exception(monkeypatch):
    _configure(monkeypatch)

    def handler(_req):
        raise ValueError("original handler error")

    mw = middleware_module.LozaMiddleware(handler, service="checkout")

    with pytest.raises(ValueError, match="original handler error"):
        mw(_Request())
