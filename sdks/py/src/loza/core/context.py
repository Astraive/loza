from __future__ import annotations

from .event import EventContext


def FromContext(ctx: object) -> tuple[EventContext | None, bool]:
    if isinstance(ctx, EventContext):
        return (ctx, True)
    return (None, False)


def HasEvent(ctx: object) -> bool:
    return isinstance(ctx, EventContext)


def EventID(ctx: EventContext) -> str:
    return ctx.event_id


def RequestIDFromContext(ctx: EventContext) -> str:
    return ctx.params.request_id or ctx.request_id


def TraceIDFromContext(ctx: EventContext) -> str:
    return ctx.trace_id or ""


def SpanIDFromContext(ctx: EventContext) -> str:
    return ctx.span_id or ""
