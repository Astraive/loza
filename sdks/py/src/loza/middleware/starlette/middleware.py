from __future__ import annotations

import time
from typing import Callable

from ... import Bool, Emit, Enrich, Finish, FinishError, Int, Params, StartHTTPEvent, String


class Middleware:
    """Starlette/ASGI middleware that captures request lifecycle as one LOZA wide event."""

    def __init__(self, app=None, service: str = "", route_resolver=None) -> None:
        self.app = app
        self.service = service
        self.route_resolver = route_resolver

    def wrap_scope(self, scope: dict) -> object:
        route = self.route_resolver(scope) if self.route_resolver else scope.get("path", "")
        return StartHTTPEvent(
            None,
            Params(
                event="http.request",
                kind="http",
                method=scope.get("method", ""),
                path=scope.get("path", ""),
                route=route,
                service=self.service,
            ),
        )

    async def __call__(self, scope: dict, receive: Callable, send: Callable) -> None:
        if self.app is None:
            raise RuntimeError(
                "loza Starlette middleware has no wrapped ASGI app; pass an app to "
                "Middleware(app, ...) or middleware(app, ...) before using it as an "
                "ASGI application (e.g. middleware(app, service='checkout'))"
            )
        if scope.get("type") != "http":
            await self.app(scope, receive, send)
            return
        status_code = 0
        response_started = False
        response_bytes = 0
        started = time.perf_counter()
        ctx = self.wrap_scope(scope)

        async def capture_send(message):
            nonlocal status_code, response_bytes, response_started
            if message.get("type") == "http.response.start":
                status_code = int(message.get("status", 200))
                response_started = True
            elif message.get("type") == "http.response.body":
                response_bytes += len(message.get("body", b""))
            await send(message)

        try:
            Enrich(
                ctx,
                String("http.user_agent", _header(scope, b"user-agent")),
                String("http.remote_ip", _remote_ip(scope)),
            )
            await self.app(scope, receive, capture_send)
            if not response_started:
                # The wrapped app returned without starting a response. No status
                # was observed and no exception was raised, so do not fabricate a
                # 500 error: record the unknown outcome with status 0 instead.
                outcome = "unknown"
            else:
                outcome = "error" if status_code >= 500 else "success"
            ctx.params.status_code = status_code
            Finish(
                ctx,
                outcome,
                Int("response_bytes", response_bytes),
                Int("duration_ms", int((time.perf_counter() - started) * 1000)),
            )
        except Exception as exc:
            FinishError(ctx, exc, Bool("panic", True))
            raise
        finally:
            # Emission is best-effort: a telemetry failure (including a sink
            # BaseException re-raised by panic recovery) must never replace an
            # application exception already in flight, nor break a successful
            # request.
            try:
                Emit(ctx)
            except BaseException:
                pass


def _header(scope, wanted):
    for key, value in scope.get("headers", []):
        if key.lower() == wanted:
            return value.decode("latin1")
    return ""


def _remote_ip(scope):
    client = scope.get("client")
    if isinstance(client, (tuple, list)) and client:
        return str(client[0])
    return ""


def middleware(app=None, **config):
    return Middleware(app, **config)
