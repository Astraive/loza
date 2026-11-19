from __future__ import annotations

from typing import get_type_hints

from loza.core.context import FromContext, HasEvent
from loza.core.event import EventContext, Params


def _event() -> EventContext:
    return EventContext(service="test", params=Params(event="test.run"))


def test_from_context_returns_none_false_for_non_event() -> None:
    # Regression: the first element must be None on failure so the
    # tuple[EventContext | None, bool] contract holds for callers that
    # ignore the boolean flag.
    for ctx in (object(), None, "not an event", 42, {"event": "x"}):
        event, ok = FromContext(ctx)
        assert event is None
        assert ok is False


def test_from_context_returns_event_true_for_event_context() -> None:
    ctx = _event()
    event, ok = FromContext(ctx)
    assert event is ctx
    assert ok is True
    assert event is not None
    assert event.event_id == ctx.event_id


def test_from_context_agrees_with_has_event() -> None:
    for ctx in (object(), None, _event()):
        event, ok = FromContext(ctx)
        assert ok is HasEvent(ctx)
        assert (event is not None) is ok


def test_from_context_return_annotation_matches_contract() -> None:
    hints = get_type_hints(FromContext)
    assert hints["return"] == tuple[EventContext | None, bool]  # type: ignore[comparison-overlap]
