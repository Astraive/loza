from __future__ import annotations

import traceback
from contextlib import contextmanager
from dataclasses import dataclass
from typing import Callable, Iterator


@dataclass(slots=True)
class RecoveredPanic:
    error: BaseException
    stack: str


@contextmanager
def recover(handler: Callable[[RecoveredPanic], None] | None = None, *, reraise: bool = True) -> Iterator[None]:
    """Recover any ``BaseException`` raised within the ``with`` block.

    This is a panic-recovery boundary, so *all* ``BaseException`` subclasses are
    caught -- including control-flow exceptions such as ``KeyboardInterrupt``,
    ``SystemExit`` and ``GeneratorExit``. The caught exception is reported to
    ``handler`` (when provided) and either re-raised or suppressed according to
    ``reraise``.
    """
    try:
        yield
    except BaseException as exc:
        recovered = RecoveredPanic(exc, traceback.format_exc())
        if handler is not None:
            handler(recovered)
        if reraise:
            raise


def run_safely(fn: Callable[[], object], handler: Callable[[RecoveredPanic], None] | None = None):
    """Run ``fn`` and return its result, or ``None`` if it raises any ``BaseException``.

    The exception is reported to ``handler`` (when provided) before being
    suppressed. Because every ``BaseException`` is recovered, control-flow
    exceptions such as ``KeyboardInterrupt`` and ``SystemExit`` also result in
    ``None`` rather than escaping the call.
    """
    with recover(handler, reraise=False):
        return fn()
    return None
