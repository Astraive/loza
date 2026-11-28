from __future__ import annotations

import json
import loza


class _SpyCtx:
    """Minimal context that records the fields each ``start_*`` call receives."""

    def __init__(self) -> None:
        self.calls: list[tuple[str, str, dict[str, object]]] = []

    def _start(self, kind: str, name: str, fields: dict[str, object]) -> _SpyCtx:
        self.calls.append((kind, name, fields))
        return self

    def start_process(self, name: str, **fields: object) -> _SpyCtx:
        return self._start("process", name, fields)

    def start_group(self, name: str, **fields: object) -> _SpyCtx:
        return self._start("group", name, fields)

    def start_timer(self, name: str, **fields: object) -> _SpyCtx:
        return self._start("timer", name, fields)


def test_timing_helpers_forward_positional_attrs():
    ctx = _SpyCtx()

    loza.with_process(ctx, "step", loza.Attr("provider", "stripe"))
    loza.with_group(ctx, "phase", loza.Attr("region", "us-east-1"))
    loza.with_timer(ctx, "span", loza.Attr("db", "postgres"))

    assert ctx.calls == [
        ("process", "step", {"provider": "stripe"}),
        ("group", "phase", {"region": "us-east-1"}),
        ("timer", "span", {"db": "postgres"}),
    ]


def test_timing_helpers_merge_positional_attrs_and_keyword_fields():
    ctx = _SpyCtx()

    loza.with_process(
        ctx,
        "step",
        loza.Attr("provider", "stripe"),
        loza.Attr("attempt", 1),
        attempt=2,
        route="/checkout",
    )

    # Keyword fields take precedence on key collisions, as in Logger.checkpoint.
    assert ctx.calls == [
        ("process", "step", {"provider": "stripe", "attempt": 2, "route": "/checkout"}),
    ]


def test_facade_helpers_forward_positional_attrs():
    ctx = _SpyCtx()

    loza.process(ctx, "step", loza.Attr("provider", "stripe"))
    loza.start_process(ctx, "step2", loza.Attr("provider", "paypal"))
    loza.timer(ctx, "t", loza.Attr("cache", "hit"))
    loza.start_timer(ctx, "t2", loza.Attr("cache", "miss"))
    loza.start_group(ctx, "g", loza.Attr("tier", "gold"))

    assert ctx.calls == [
        ("process", "step", {"provider": "stripe"}),
        ("process", "step2", {"provider": "paypal"}),
        ("timer", "t", {"cache": "hit"}),
        ("timer", "t2", {"cache": "miss"}),
        ("group", "g", {"tier": "gold"}),
    ]


def test_real_context_emits_start_attributes_and_completion_attributes_win():
    logger, sink = loza.TestLogger()
    ctx = logger.start_event(loza.Params(event="test.timing.attrs"))

    process = loza.with_process(
        ctx,
        "step",
        loza.Attr("provider", "stripe"),
        attempt=1,
    )
    process.finish(attempt=2, phase="completed")
    group = loza.with_group(
        ctx,
        "phase",
        loza.Attr("region", "us-east-1"),
        tier="gold",
    )
    group.finish(region="us-west-2")
    timer = loza.with_timer(
        ctx,
        "span",
        loza.Attr("db", "postgres"),
        operation="query",
    )
    timer.stop(operation="fetch")

    logger.finish(ctx, "success")
    logger.emit(ctx)
    payload = json.loads(sink.events[0])

    assert payload["processes"][0]["provider"] == "stripe"
    assert payload["processes"][0]["attempt"] == 2
    assert payload["processes"][0]["phase"] == "completed"
    assert payload["groups"][0]["region"] == "us-west-2"
    assert payload["groups"][0]["tier"] == "gold"
    assert payload["timers"][0]["db"] == "postgres"
    assert payload["timers"][0]["operation"] == "fetch"
