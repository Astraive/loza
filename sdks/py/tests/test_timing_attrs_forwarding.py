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

def test_timing_attr_policies_apply_to_entries_and_registered_keys():
    logger, sink = loza.TestLogger()
    ctx = logger.start_event(loza.Params(event="test.timing.attrs.policies"))
    logger.enrich(ctx, loza.Attr("registered_policy", "registered-secret", sensitive=True))

    process = loza.with_process(
        ctx,
        "step",
        loza.Attr("process_secret", "process-secret", sensitive=True),
        loza.Attr("process_hash", "process-hash", hash_value=True),
        loza.Attr("process_drop", "process-drop", drop=True),
        registered_policy="process-registered",
    )
    process.finish()
    group = loza.with_group(
        ctx,
        "phase",
        loza.Attr("group_secret", "group-secret", sensitive=True),
        loza.Attr("group_hash", "group-hash", hash_value=True),
        loza.Attr("group_drop", "group-drop", drop=True),
        registered_policy="group-registered",
    )
    group.finish()
    timer = loza.with_timer(
        ctx,
        "span",
        loza.Attr("timer_secret", "timer-secret", sensitive=True),
        loza.Attr("timer_hash", "timer-hash", hash_value=True),
        loza.Attr("timer_drop", "timer-drop", drop=True),
        registered_policy="timer-registered",
    )
    timer.stop()

    logger.finish(ctx, "success")
    logger.emit(ctx)
    payload = json.loads(sink.events[0])

    for section, prefix in (
        ("processes", "process"),
        ("groups", "group"),
        ("timers", "timer"),
    ):
        entry = payload[section][0]
        assert entry[f"{prefix}_secret"] == "[REDACTED]"
        assert entry[f"{prefix}_hash"] != f"{prefix}-hash"
        assert f"{prefix}_drop" not in entry
        assert entry["registered_policy"] == "[REDACTED]"
    assert "registered-secret" not in sink.events[0]
    assert "process-secret" not in sink.events[0]
    assert "group-secret" not in sink.events[0]
    assert "timer-secret" not in sink.events[0]

def test_timing_path_policies_preserve_reserved_fields():
    logger, sink = loza.TestLogger()
    ctx = logger.start_event(loza.Params(event="test.timing.path.policies"))
    logger.enrich(
        ctx,
        loza.Attr("account.token", "registered-token", sensitive=True),
        loza.Attr("account.hash", "registered-hash", hash_value=True),
        loza.Attr("account.drop", "registered-drop", drop=True),
        loza.Attr("name", "registered-name", sensitive=True),
        loza.Attr("duration_ms", 500, drop=True),
        loza.Attr("step", 500, hash_value=True),
        loza.Attr("status_code", 500, sensitive=True),
    )

    def account(prefix: str) -> dict[str, str]:
        return {
            "token": f"{prefix}-token",
            "hash": f"{prefix}-hash",
            "drop": f"{prefix}-drop",
        }

    process = loza.with_process(
        ctx, "process-name", account=account("process"), step=999, duration_ms=999
    )
    process.finish(status_code=201)
    group = loza.with_group(
        ctx, "group-name", account=account("group"), step=999, duration_ms=999
    )
    group.finish(status_code=202)
    timer = loza.with_timer(
        ctx, "timer-name", account=account("timer"), step=999, duration_ms=999
    )
    timer.stop(status_code=203)

    logger.finish(ctx, "success")
    logger.emit(ctx)
    payload = json.loads(sink.events[0])

    expected = (
        ("processes", "process-name", 201, "process"),
        ("groups", "group-name", 202, "group"),
        ("timers", "timer-name", 203, "timer"),
    )
    for section, name, status_code, prefix in expected:
        entry = payload[section][0]
        assert entry["name"] == name
        assert isinstance(entry["duration_ms"], int)
        assert entry["status_code"] == status_code
        assert entry["account"]["token"] == "[REDACTED]"
        assert entry["account"]["hash"] != f"{prefix}-hash"
        assert "drop" not in entry["account"]
    assert payload["processes"][0]["step"] == 1
    for secret in (
        "registered-token",
        "registered-hash",
        "registered-name",
        "process-token",
        "process-hash",
        "process-drop",
        "group-token",
        "group-hash",
        "group-drop",
        "timer-token",
        "timer-hash",
        "timer-drop",
    ):
        assert secret not in sink.events[0]


def test_facade_timing_policies_apply_before_completion_overrides():
    logger, sink = loza.TestLogger()
    ctx = logger.start_event(loza.Params(event="test.facade.timing.policies"))

    loza.process(
        ctx,
        "process",
        loza.Attr("process_secret", "process-start-secret", sensitive=True),
    ).finish(process_secret="process-completion-secret")
    loza.start_process(
        ctx,
        "start_process",
        loza.Attr("process_hash", "process-start-hash", hash_value=True),
    ).finish(process_hash="process-completion-hash")
    loza.timer(
        ctx,
        "timer",
        loza.Attr("timer_secret", "timer-start-secret", sensitive=True),
    ).stop(timer_secret="timer-completion-secret")
    loza.start_timer(
        ctx,
        "start_timer",
        loza.Attr("timer_drop", "timer-start-drop", drop=True),
    ).stop(timer_drop="timer-completion-drop")
    loza.start_group(
        ctx,
        "group",
        loza.Attr("group_secret", "group-start-secret", sensitive=True),
    ).finish(group_secret="group-completion-secret")

    logger.finish(ctx, "success")
    logger.emit(ctx)
    payload = json.loads(sink.events[0])

    assert payload["processes"][0]["process_secret"] == "[REDACTED]"
    assert payload["processes"][1]["process_hash"] != "process-completion-hash"
    assert payload["timers"][0]["timer_secret"] == "[REDACTED]"
    assert "timer_drop" not in payload["timers"][1]
    assert payload["groups"][0]["group_secret"] == "[REDACTED]"
    for secret in (
        "process-start-secret",
        "process-completion-secret",
        "process-start-hash",
        "process-completion-hash",
        "timer-start-secret",
        "timer-completion-secret",
        "timer-start-drop",
        "timer-completion-drop",
        "group-start-secret",
        "group-completion-secret",
    ):
        assert secret not in sink.events[0]
