"""Regression tests for the Loguru integration's ``bind_loza`` helper."""

from __future__ import annotations


class _FakeLogger:
    """Minimal stand-in for ``loguru.logger`` that records ``add`` calls."""

    def __init__(self) -> None:
        self.added: list[object] = []

    def add(self, sink: object, **kwargs: object) -> int:
        self.added.append(sink)
        return len(self.added)


def test_bind_loza_attaches_the_loza_sink() -> None:
    from loza.integrations.loguru.adapter import LoguruSink, bind_loza

    logger = _FakeLogger()

    result = bind_loza(logger)

    # The logger is returned for chaining/drop-in compatibility...
    assert result is logger
    # ...but, unlike the old no-op, it is actually wired up to Loza.
    assert len(logger.added) == 1
    assert isinstance(logger.added[0], LoguruSink)
