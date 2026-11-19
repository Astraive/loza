from __future__ import annotations

from .event import EventContext, Params


def NewEvent(params: Params, service: str = "") -> EventContext:
    return EventContext(service=service, params=params)
