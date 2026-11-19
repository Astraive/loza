from __future__ import annotations

import time
from typing import Any, Awaitable, Callable

from ... import Bool, Emit, Enrich, Finish, FinishError, Int, Params, StartHTTPEvent, String
from .response import ResponseCapture
from ... import Emit, Enrich, Finish, FinishError, Int, Params, StartHTTPEvent, String


class Middleware:
    """ASGI middleware that captures request lifecycle as one LOZA wide event."""

    def __init__(
        self, app=None, service: str = "", route_resolver: Callable[[dict[str, Any]], str] | None = None
    ) -> None:
        self.app = app
        self.service = service
        self.route_resolver = route_resolver

    def _event_context(self, scope: dict[str, Any], route: str) -> object:
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

    def wrap_scope(self, scope: dict[str, Any]) -> object:
        route = self.route_resolver(scope) if self.route_resolver else scope.get("path", "")
        return self._event_context(scope, route)

    async def __call__(
        self,
        scope: dict[str, Any],
        receive: Callable[[], Awaitable[dict[str, Any]]],
        send: Callable[[dict[str, Any]], Awaitable[None]],
    ) -> None:
        if self.app is None:
            return
        if scope.get("type") != "http":
            await self.app(scope, receive, send)
            return

        capture = ResponseCapture()
        started = time.perf_counter()
        ctx: object | None = None

        async def capture_send(message: dict[str, Any]) -> None:
            if message.get("type") == "http.response.start":
                capture.record(status_code=int(message.get("status", 200)))
            elif message.get("type") == "http.response.body":
                capture.record(body_bytes=len(message.get("body", b"")))
            await send(message)

        try:
            ctx = self.wrap_scope(scope)
            Enrich(
                ctx,
                String("http.user_agent", _header(scope, b"user-agent")),
                String("http.remote_ip", _remote_ip(scope)),
            )
            await self.app(scope, receive, capture_send)
            ctx.params.status_code = status_code
            Finish(
                ctx,
                "error" if status_code >= 500 else "success",
                Int("response_bytes", response_bytes),
                "error" if capture.status_code >= 500 else "success",
                Int("status_code", capture.status_code),
                Int("response_bytes", capture.bytes),
                Int("duration_ms", int((time.perf_counter() - started) * 1000)),
            )
        except BaseException as exc:
            # asyncio.CancelledError (a BaseException since 3.8) must finish the
            # event with an error too, otherwise `finally` emits it incomplete.
        except Exception as exc:
            if ctx is None:
                # route_resolver raised before the event existed. Fall back to
                # the raw path so the failure is still recorded and emitted.
                ctx = self._event_context(scope, scope.get("path", ""))
            FinishError(ctx, exc, Bool("panic", True))
            FinishError(ctx, exc)
            raise
        finally:
            if ctx is not None:
                Emit(ctx)


def _header(scope: dict[str, Any], wanted: bytes) -> str:
    for key, value in scope.get("headers", []):
        if key.lower() == wanted:
            return value.decode("latin1")
    return ""


def _remote_ip(scope: dict[str, Any]) -> str:
    client = scope.get("client")
    if isinstance(client, (tuple, list)) and client:
        return str(client[0])
    return ""


def middleware(app=None, **config):
    return Middleware(app, **config)
