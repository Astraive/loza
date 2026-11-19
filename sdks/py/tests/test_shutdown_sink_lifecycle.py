from __future__ import annotations

import pytest

import loza
from loza.core.client import Logger as ClientLogger
from loza.core.logger import Logger as CoreLogger


class _CloseCountingSink:
    """A non-idempotent sink: each close() call is recorded."""

    def __init__(self) -> None:
        self.close_calls = 0
        self.flush_calls = 0
        self.events: list[str] = []

    def write_batch(self, events: list[str]) -> None:
        self.events.extend(events)

    def flush(self) -> None:
        self.flush_calls += 1

    def close(self) -> None:
        self.close_calls += 1


@pytest.mark.parametrize("logger_cls", [ClientLogger, CoreLogger])
def test_shutdown_closes_sink_once_with_pipeline(logger_cls) -> None:
    """Pipeline.close owns sink lifecycle; shutdown must not close sinks again."""
    sink = _CloseCountingSink()
    logger = logger_cls(loza.Config.test("shutdown-async").with_sink(sink).with_async(True))
    assert logger._pipeline is not None

    logger.shutdown()

    assert sink.close_calls == 1


@pytest.mark.parametrize("logger_cls", [ClientLogger, CoreLogger])
def test_shutdown_closes_sink_once_without_pipeline(logger_cls) -> None:
    """Without a pipeline, shutdown still flushes then closes the sink exactly once."""
    sink = _CloseCountingSink()
    logger = logger_cls(loza.Config.test("shutdown-sync").with_sink(sink))
    assert logger._pipeline is None

    logger.shutdown()

    assert sink.flush_calls == 1
    assert sink.close_calls == 1
