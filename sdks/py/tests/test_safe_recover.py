"""Regression tests for the ``loza.safe`` panic-recovery helpers.

``recover``/``run_safely`` advertise a ``BaseException`` contract (see the
``RecoveredPanic.error`` annotation), so control-flow exceptions such as
``KeyboardInterrupt``, ``SystemExit`` and ``GeneratorExit`` must be recovered
just like ordinary ``Exception`` subclasses.
"""

from __future__ import annotations

import pytest

from loza.internal.safe import recover as internal_recover
from loza.internal.safe import run_safely as internal_run_safely
from loza.safe import recover, run_safely
from loza.safe.recover import RecoveredPanic


class CustomBaseException(BaseException):
    """A user exception that derives directly from ``BaseException``."""


# Both the public and the internal module expose the same helper; keep them in
# lockstep so the adjacent implementation cannot regress independently.
MODULES = [
    pytest.param(run_safely, recover, id="public"),
    pytest.param(internal_run_safely, internal_recover, id="internal"),
]

CONTROL_FLOW_EXCEPTIONS = [
    KeyboardInterrupt(),
    SystemExit(2),
    GeneratorExit(),
    CustomBaseException("panic"),
]


def _raise(exc: BaseException):
    def raiser():
        raise exc

    return raiser


@pytest.mark.parametrize("run_safely_fn,recover_fn", MODULES)
@pytest.mark.parametrize("exc", CONTROL_FLOW_EXCEPTIONS, ids=lambda exc: type(exc).__name__)
def test_run_safely_recovers_all_base_exceptions(run_safely_fn, recover_fn, exc):
    """run_safely returns None instead of letting control-flow exceptions escape."""
    recovered: list[RecoveredPanic] = []

    result = run_safely_fn(_raise(exc), recovered.append)

    assert result is None
    assert len(recovered) == 1
    assert recovered[0].error is exc
    assert isinstance(recovered[0].error, BaseException)
    assert recovered[0].stack  # a traceback was captured


@pytest.mark.parametrize("run_safely_fn,recover_fn", MODULES)
def test_run_safely_returns_result_and_skips_handler_on_success(run_safely_fn, recover_fn):
    recovered: list[RecoveredPanic] = []

    assert run_safely_fn(lambda: "ok", recovered.append) == "ok"
    assert recovered == []


@pytest.mark.parametrize("run_safely_fn,recover_fn", MODULES)
def test_run_safely_without_handler_still_suppresses(run_safely_fn, recover_fn):
    assert run_safely_fn(_raise(SystemExit(1))) is None


@pytest.mark.parametrize("run_safely_fn,recover_fn", MODULES)
@pytest.mark.parametrize("exc", CONTROL_FLOW_EXCEPTIONS, ids=lambda exc: type(exc).__name__)
def test_recover_reraise_true_reports_then_reraises(run_safely_fn, recover_fn, exc):
    """With reraise=True the handler still sees the panic before it propagates."""
    recovered: list[RecoveredPanic] = []

    with pytest.raises(type(exc)) as caught:
        with recover_fn(recovered.append):
            raise exc

    assert caught.value is exc
    assert len(recovered) == 1
    assert recovered[0].error is exc


@pytest.mark.parametrize("run_safely_fn,recover_fn", MODULES)
def test_recover_reraise_false_suppresses(run_safely_fn, recover_fn):
    recovered: list[RecoveredPanic] = []

    with recover_fn(recovered.append, reraise=False):
        raise KeyboardInterrupt()

    assert len(recovered) == 1
    assert isinstance(recovered[0].error, KeyboardInterrupt)


def test_recovered_panic_error_is_typed_base_exception():
    """The public type contract explicitly allows any BaseException."""
    import typing

    hints = typing.get_type_hints(RecoveredPanic)
    assert hints["error"] is BaseException
