from __future__ import annotations

from loza.core import event_api
from loza.core.event import EventContext, Params


def test_new_event_returns_event_context_with_service_and_params() -> None:
    params = Params(event="test.run", kind="cli", message="hello")

    ctx = event_api.NewEvent(params, service="svc-a")

    assert isinstance(ctx, EventContext)
    assert ctx.service == "svc-a"
    assert ctx.params is params


def test_new_event_has_no_dead_logger_path() -> None:
    # The former dead `if False` Logger branch and its unused import are gone.
    assert not hasattr(event_api, "Logger")
