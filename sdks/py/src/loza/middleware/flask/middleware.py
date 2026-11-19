from __future__ import annotations

import time
from typing import Callable, Iterable

from ... import Bool, Emit, Enrich, Finish, FinishError, Int, Params, StartHTTPEvent, String


class Middleware:
    """Flask/WSGI middleware that captures request lifecycle as one LOZA wide event."""

    def __init__(self, app=None, service: str = "", **config) -> None:
        self.app = app
        self.service = service
        self.config = config

    def wrap_scope(self, environ: dict) -> object:
        return StartHTTPEvent(
            None,
            Params(
                event="http.request",
                kind="http",
                method=environ.get("REQUEST_METHOD", ""),
                path=environ.get("PATH_INFO", ""),
                route=environ.get("PATH_INFO", ""),
                service=self.service,
            ),
        )

    def _respond_without_app(self, environ: dict, start_response: Callable) -> Iterable[bytes]:
        """Return a WSGI-valid 500 and emit a LOZA error event when no app is configured.

        ``app=None`` is the default, so a common miswire (``wsgi_app = middleware()``)
        would otherwise return an empty iterable without ever calling
        ``start_response``, which WSGI servers reject and which emits no event.
        """
        start_response("500 Internal Server Error", [("Content-Type", "text/plain")])
        ctx = self.wrap_scope(environ)
        FinishError(
            ctx,
            RuntimeError("loza.flask.middleware: no WSGI app configured (app=None)"),
            Bool("panic", True),
            Int("status_code", 500),
        )
        Emit(ctx)
        return [b"Internal Server Error"]

    def __call__(self, environ: dict, start_response: Callable) -> Iterable[bytes]:
        if self.app is None:
            return self._respond_without_app(environ, start_response)
        status_code = 500
        response_bytes = 0
        started = time.perf_counter()
        ctx = self.wrap_scope(environ)

        def capture_start_response(status, response_headers, exc_info=None):
            nonlocal status_code
            status_code = int(status.split()[0]) if status else 500
            return start_response(status, response_headers, exc_info)

        try:
            Enrich(
                ctx,
                String("http.user_agent", environ.get("HTTP_USER_AGENT", "")),
                String("http.remote_ip", environ.get("REMOTE_ADDR", "")),
            )
            response = self.app(environ, capture_start_response)
            ctx.params.status_code = status_code
        except Exception as exc:
            FinishError(ctx, exc, Bool("panic", True))
            Emit(ctx)
            raise

        def stream_response() -> Iterable[bytes]:
            nonlocal response_bytes
            finalized = False
            aborted = False
            try:
                for chunk in response:
                    if isinstance(chunk, bytes):
                        response_bytes += len(chunk)
                        yield chunk
                    else:
                        encoded_chunk = chunk.encode("utf-8")
                        response_bytes += len(encoded_chunk)
                        yield encoded_chunk
                Finish(
                    ctx,
                    "error" if status_code >= 500 else "success",
                    Int("response_bytes", response_bytes),
                    Int("duration_ms", int((time.perf_counter() - started) * 1000)),
                )
                finalized = True
            except Exception as exc:
                FinishError(ctx, exc, Bool("panic", True))
                finalized = True
                raise
            except GeneratorExit:
                # The consumer closed the iterator early (e.g. a client
                # disconnect). A truncated response is not a success.
                aborted = True
                raise
            except BaseException as exc:
                # KeyboardInterrupt/SystemExit/etc. bypass `except Exception`;
                # record them instead of letting the fallback call Finish as
                # if the stream had completed normally.
                FinishError(ctx, exc, Bool("panic", True))
                finalized = True
                raise
            finally:
                try:
                    if not finalized:
                        # "cancelled" is the canonical outcome for a response
                        # whose consumer went away before it finished.
                        outcome = "cancelled" if aborted else ("error" if status_code >= 500 else "success")
                        Finish(
                            ctx,
                            "error" if status_code >= 500 else "success",
                            outcome,
                            Int("status_code", status_code),
                            Int("response_bytes", response_bytes),
                            Int("duration_ms", int((time.perf_counter() - started) * 1000)),
                        )
                    close = getattr(response, "close", None)
                    if callable(close):
                        close()
                finally:
                    Emit(ctx)

        return stream_response()


def middleware(app=None, **config):
    return Middleware(app, **config)
