from __future__ import annotations

import time

from ... import Bool, Emit, Enrich, Finish, FinishError, Int, Params, StartHTTPEvent, String


def _route_from_match(request) -> str:
    """Return the matched route template, or "" when the request was not resolved."""
    match = getattr(request, "resolver_match", None)
    if match is None:
        return ""
    return str(getattr(match, "route", "") or getattr(match, "view_name", "") or "")


class LozaMiddleware:
    """Django middleware that captures request lifecycle as one LOZA wide event."""

    def __init__(self, get_response=None, service: str = "") -> None:
        self.get_response = get_response
        self.service = service

    def __call__(self, request):
        started = time.perf_counter()
        ctx = StartHTTPEvent(
            None,
            Params(
                event="http.request",
                kind="http",
                method=request.method or "",
                path=request.path,
                route=_route_from_match(request) or request.path,
                service=self.service,
            ),
        )
        try:
            Enrich(
                ctx,
                String("http.user_agent", request.META.get("HTTP_USER_AGENT", "")),
                String("http.remote_ip", request.META.get("REMOTE_ADDR", "")),
            )
            response = self.get_response(request)
            ctx.params.status_code = response.status_code
            Finish(
                ctx,
                "error" if response.status_code >= 500 else "success",
                Int("duration_ms", int((time.perf_counter() - started) * 1000)),
            )
            return response
        except Exception as exc:
            FinishError(ctx, exc, Bool("panic", True))
            raise
        finally:
            # Django resolves the URL while dispatching the view, after this
            # middleware has been entered. resolver_match is populated by then,
            # so prefer the route template over the high-cardinality path.
            route = _route_from_match(request)
            if route:
                ctx.params.route = route
            Emit(ctx)


def middleware(get_response=None, **config):
    return LozaMiddleware(get_response, **config)
