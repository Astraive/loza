"""Regression tests for Flask streaming-response finalization.

An aborted stream (client disconnect / iterator.close()) or an unexpected
BaseException during streaming must never be reported as a successful request.
"""

from __future__ import annotations

import json

import loza
from loza.middleware.flask import Middleware as FlaskMiddleware


def _middleware(app) -> FlaskMiddleware:
    sink = loza.MemorySink()
    loza.Configure(loza.Test("checkout").with_sink(sink))
    return FlaskMiddleware(app, service="checkout"), sink


def _environ() -> dict:
    return {"REQUEST_METHOD": "GET", "PATH_INFO": "/stream"}


def test_aborted_stream_is_reported_as_cancelled() -> None:
    """Closing the response iterator early must not finalize as 'success'."""

    def app(_environ, start_response):
        start_response("200 OK", [])
        return iter([b"one", b"two"])

    middleware, sink = _middleware(app)
    iterator = iter(middleware(_environ(), lambda *_args: None))

    assert next(iterator) == b"one"
    iterator.close()

    assert len(sink.events) == 1
    payload = json.loads(sink.events[0])
    assert payload["outcome"] == "cancelled", payload
    assert payload["attrs"]["response_bytes"] == 3
    assert "error" not in payload


def test_base_exception_during_stream_is_recorded_as_error() -> None:
    """A BaseException (not an Exception) must be recorded and still propagate."""

    class Interrupted(BaseException):
        pass

    def app(_environ, start_response):
        start_response("200 OK", [])

        def body():
            yield b"first"
            raise Interrupted("client gone")

        return body()

    middleware, sink = _middleware(app)
    iterator = iter(middleware(_environ(), lambda *_args: None))

    assert next(iterator) == b"first"
    try:
        next(iterator)
    except Interrupted:
        pass
    else:  # pragma: no cover
        raise AssertionError("BaseException should propagate to the caller")

    assert len(sink.events) == 1
    payload = json.loads(sink.events[0])
    assert payload["outcome"] == "error", payload
    assert payload["error"]["type"] == "Interrupted"
    assert payload["error"]["message"] == "client gone"


def test_completed_stream_is_still_reported_as_success() -> None:
    """A fully consumed 2xx stream keeps the successful outcome."""

    def app(_environ, start_response):
        start_response("200 OK", [])
        return iter([b"one", b"two"])

    middleware, sink = _middleware(app)

    assert list(middleware(_environ(), lambda *_args: None)) == [b"one", b"two"]

    assert len(sink.events) == 1
    payload = json.loads(sink.events[0])
    assert payload["outcome"] == "success", payload


def test_lazy_application_records_status_from_start_response_during_iteration() -> None:
    """A generator WSGI app may start its response only when first iterated."""

    def app(_environ, start_response):
        def body():
            start_response("201 Created", [])
            yield b"created"

        return body()

    middleware, sink = _middleware(app)

    assert list(middleware(_environ(), lambda *_args: None)) == [b"created"]

    assert len(sink.events) == 1
    payload = json.loads(sink.events[0])
    assert payload["outcome"] == "success"
    assert payload["status_code"] == 201
    assert payload["http"]["status_code"] == 201
