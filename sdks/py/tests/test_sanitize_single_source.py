from __future__ import annotations

import loza
from loza.core import event as event_module
from loza.core import logger as logger_module
from loza.core.logger import Logger


def _make_ctx() -> event_module.EventContext:
    ctx = event_module.EventContext(service="api", params=loza.Params(event="sanitize"))
    ctx.attrs["card"] = "4111111111111111"
    ctx.attrs["nested"] = {"token": "secret"}
    ctx.user["email"] = "user@example.test"
    ctx.tenant["id"] = "tenant-1"
    ctx._drop_keys.add("nested.token")
    ctx._hash_keys.add("user.email")
    ctx._sensitive_keys.add("tenant.id")
    return ctx


def test_logger_sanitize_event_matches_standalone() -> None:
    logger = Logger(loza.Config.test("sanitize-match"))
    ctx = _make_ctx()

    expected = event_module.sanitize_event(ctx)
    actual = logger._sanitize_event(ctx)

    assert actual is not ctx
    assert "token" not in actual.attrs["nested"]
    assert actual.user["email"] != "user@example.test"
    assert actual.tenant["id"] == "[REDACTED]"
    # The original context must never be mutated.
    assert ctx.user["email"] == "user@example.test"
    assert ctx.tenant["id"] == "tenant-1"
    assert actual.attrs == expected.attrs
    assert actual.user == expected.user
    assert actual.tenant == expected.tenant


def test_logger_sanitize_event_delegates_to_standalone(monkeypatch) -> None:
    """Logger must use sanitize_event as the single source of truth."""
    logger = Logger(loza.Config.test("sanitize-delegation"))
    ctx = _make_ctx()

    calls: list[event_module.EventContext] = []

    def sentinel(arg: event_module.EventContext) -> event_module.EventContext:
        calls.append(arg)
        return arg

    monkeypatch.setattr(logger_module, "sanitize_event", sentinel)

    assert logger._sanitize_event(ctx) is ctx
    assert calls == [ctx]
