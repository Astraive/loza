from __future__ import annotations

import json

import loza
import pytest


def test_lifecycle_event_helpers():
    logger, sink = loza.TestLogger()
    ctx = logger.start_event(loza.Params(event="checkout.request"))
    logger.enrich(ctx, user_id="u_123", tenant_id="t_123")
    logger.enrich(ctx, payment_provider="stripe")
    logger.checkpoint(ctx, "validated")

    cloned = logger.clone_event(ctx)
    assert cloned.event_id == ctx.event_id
    assert cloned.params.event == "checkout.request"

    parent = logger.start_event(loza.Params(event="parent", trace_id="trace1"))
    child = logger.start_event(loza.Params(event="child", trace_id="trace2"))
    logger.link_event(parent, child)
    assert parent.params.links == [child.event_id]

    logger.finish(ctx, "success")
    logger.emit(ctx)
    payload = json.loads(sink.events[0])
    assert payload["outcome"] == "success"


def test_lifecycle_outcome_helpers():
    logger, _ = loza.TestLogger()

    ctx = logger.start_event(loza.Params(event="test.drop"))
    logger.drop(ctx)
    assert ctx.outcome == "abandoned"
    assert ctx.emitted

    ctx = logger.start_event(loza.Params(event="test.cancel"))
    logger.cancel(ctx)
    assert ctx.outcome == "cancelled"

    ctx = logger.start_event(loza.Params(event="test.abandon"))
    logger.abandon(ctx)
    assert ctx.outcome == "abandoned"

    ctx = logger.start_event(loza.Params(event="test.retry"))
    logger.retry(ctx)
    assert ctx.outcome == "retried"

    ctx = logger.start_event(loza.Params(event="test.partial"))
    logger.partial(ctx, reason="timeout")
    assert ctx.partial
    assert ctx.partial_reason == "timeout"


class _FlakySink:
    """Sink that fails writes until ``fail`` is flipped to False."""

    def __init__(self) -> None:
        self.events: list[str] = []
        self.fail = True

    def write(self, payload: str) -> None:
        if self.fail:
            raise IOError("sink unavailable")
        self.events.append(payload)

    def flush(self) -> None:
        pass

    def close(self) -> None:
        pass


def test_retry_reopens_delivery_failed_event_for_reemission():
    """A delivery-failed event must be reopenable so emit() can resend it."""
    sink = _FlakySink()
    logger = loza.New(loza.Test("retry").with_sink(sink))
    ctx = logger.start_event(loza.Params(event="retry.delivery"))
    logger.finish(ctx, "success")

    assert logger.emit(ctx) == ""
    assert ctx.event_state == "delivery_failed"
    assert not ctx.emitted

    logger.retry(ctx)
    assert ctx.outcome == "retried"
    assert ctx.event_state == "finished"
    assert not ctx.emitted

    sink.fail = False
    payload = logger.emit(ctx)
    assert payload
    assert sink.events == [payload]
    assert ctx.emitted and ctx.event_state == "emitted"


def test_retry_on_delivery_failed_event_without_prior_finish():
    """retry() also closes the event when emit() failed before finish()."""
    sink = _FlakySink()
    logger = loza.New(loza.Test("retry").with_sink(sink))
    ctx = logger.start_event(loza.Params(event="retry.unfinished"))

    assert logger.emit(ctx) == ""
    assert ctx.event_state == "delivery_failed"
    assert ctx.finished_at is None

    logger.retry(ctx, loza.String("attempt", "2"))
    assert ctx.outcome == "retried"
    assert ctx.finished_at is not None

    sink.fail = False
    payload = logger.emit(ctx)
    assert payload
    assert json.loads(payload)["attrs"]["attempt"] == "2"


def test_retry_reopens_emitted_event_for_redelivery():
    """retry() clears the emitted flag so a delivered event can be re-sent."""
    logger, sink = loza.TestLogger("retry")
    ctx = logger.start_event(loza.Params(event="retry.emitted"))
    logger.finish(ctx, "success")

    first = logger.emit(ctx)
    assert first
    assert logger.emit(ctx) == first  # idempotent while emitted

    logger.retry(ctx)
    assert not ctx.emitted
    assert ctx.event_state == "finished"

    second = logger.emit(ctx)
    assert second
    assert len(sink.events) == 2
    assert json.loads(sink.events[1])["outcome"] == "retried"


def test_lifecycle_wrap_helpers():
    logger, sink = loza.TestLogger()
    ctx = logger.start_event(loza.Params(event="test.wrap"))
    result = logger.wrap(ctx, lambda x: x + 1, 41)
    assert result == 42
    logger.emit(ctx)
    payload = json.loads(sink.events[0])
    assert payload["outcome"] == "success"

    ctx = logger.start_event(loza.Params(event="test.wrap.fail"))
    try:
        logger.wrap(ctx, lambda: 1 / 0)
    except ZeroDivisionError:
        pass
    logger.emit(ctx)
    payload = json.loads(sink.events[-1])
    assert payload["outcome"] == "error"


def test_finished_event_rejects_further_mutation():
    """Mutators must not append data to an event after it has been finished."""
    logger, _ = loza.TestLogger()
    ctx = logger.start_event(loza.Params(event="mutate.finished"))
    logger.finish(ctx, "success")
    before = ctx.to_dict()

    for mutate in (
        lambda: ctx.checkpoint("too-late"),
        lambda: ctx.start_process("late-process"),
        lambda: ctx.start_timer("late-timer"),
        lambda: ctx.start_group("late-group"),
        lambda: logger.enrich(ctx, leaked="value"),
        lambda: logger.set(ctx, leaked="value"),
        lambda: logger.merge(ctx, "user", leaked="value"),
        lambda: logger.delete(ctx, "leaked"),
    ):
        with pytest.raises(loza.EventClosedError):
            mutate()

    assert ctx.to_dict() == before


def test_partial_event_rejects_further_mutation():
    """mark_partial() also closes the event, even without finished_at."""
    logger, _ = loza.TestLogger()
    ctx = logger.start_event(loza.Params(event="mutate.partial"))
    logger.partial(ctx, reason="timeout")
    assert ctx.finished_at is None

    with pytest.raises(loza.EventClosedError):
        ctx.checkpoint("too-late")
    with pytest.raises(loza.EventClosedError):
        logger.enrich(ctx, leaked="value")
    assert ctx.checkpoints == []
    assert ctx.attrs == {}


def test_double_finish_keeps_dedicated_error():
    """Closing finished events must not lose the double-finish error type."""
    logger, _ = loza.TestLogger()
    ctx = logger.start_event(loza.Params(event="mutate.double-finish"))
    logger.finish(ctx, "success")

    with pytest.raises(loza.EventAlreadyFinishedError):
        logger.finish(ctx, "again")
    with pytest.raises(loza.EventAlreadyFinishedError):
        logger.finish_error(ctx, ValueError("boom"))


def test_retry_with_attrs_on_delivered_event():
    """Retry still applies attrs and reopens an already finished event."""
    logger, sink = loza.TestLogger()
    ctx = logger.start_event(loza.Params(event="retry.attrs"))
    logger.finish(ctx, "success")
    assert logger.emit(ctx)

    logger.retry(ctx, loza.String("attempt", "2"))
    assert ctx.event_state == "finished"
    assert not ctx.emitted

    second = logger.emit(ctx)
    assert second
    assert len(sink.events) == 2
    assert json.loads(second)["attrs"]["attempt"] == "2"
