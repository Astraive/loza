"""Regression tests for run()/run_event() control-flow propagation.

Control-flow BaseExceptions (KeyboardInterrupt, SystemExit, GeneratorExit)
raised by a callback must reach the caller so Ctrl+C / sys.exit() can
terminate the process. The event must still be finished and emitted first.
Ordinary Exception subclasses keep their historical behavior of being
recorded and returning the emitted payload.
"""

from __future__ import annotations

import pytest

import loza


def _throw(exc: BaseException):
    def fn(_ctx: loza.EventContext):
        raise exc

    return fn


@pytest.mark.parametrize("exc", [KeyboardInterrupt(), SystemExit(3), GeneratorExit()])
def test_run_reraises_control_flow_exceptions_after_emit(exc: BaseException) -> None:
    ctx = loza.start_event(loza.Params(event="run.control_flow"))
    with pytest.raises(type(exc)) as caught:
        loza.run(ctx, _throw(exc))

    assert caught.value is exc
    # The event is still recorded as an error and emitted before re-raising.
    assert ctx.outcome == "error"
    assert ctx.finished_at is not None
    assert ctx.emitted is True
    assert ctx.error["type"] == type(exc).__name__


@pytest.mark.parametrize("exc", [KeyboardInterrupt(), SystemExit(3), GeneratorExit()])
def test_run_event_reraises_control_flow_exceptions(exc: BaseException) -> None:
    with pytest.raises(type(exc)) as caught:
        loza.run_event(loza.Params(event="run_event.control_flow"), _throw(exc))

    assert caught.value is exc


def test_run_swallows_ordinary_exceptions() -> None:
    ctx = loza.start_event(loza.Params(event="run.ordinary_error"))
    emitted = loza.run(ctx, _throw(RuntimeError("boom")))

    assert ctx.outcome == "error"
    assert ctx.error["type"] == "RuntimeError"
    assert isinstance(emitted, str)


def test_run_event_swallows_ordinary_exceptions() -> None:
    emitted = loza.run_event(loza.Params(event="run_event.ordinary_error"), _throw(RuntimeError("boom")))

    assert isinstance(emitted, str)
