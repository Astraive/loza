"""Regression tests for the structlog integration's ``bind_loza`` helper."""

from __future__ import annotations

import sys
from types import ModuleType

import pytest


class _FakeStructlog:
    """Minimal stand-in for the optional ``structlog`` module."""

    def __init__(self, processors: list[object] | None = None) -> None:
        self._processors = list(processors) if processors is not None else []
        self.configure_calls: list[dict[str, object]] = []

    def get_config(self) -> dict[str, object]:
        return {"processors": list(self._processors)}

    def configure(self, **kwargs: object) -> None:
        self.configure_calls.append(kwargs)
        self._processors = list(kwargs.get("processors") or ())


@pytest.fixture
def fake_structlog(monkeypatch: pytest.MonkeyPatch) -> _FakeStructlog:
    module = ModuleType("structlog")
    fake = _FakeStructlog()
    module.get_config = fake.get_config  # type: ignore[attr-defined]
    module.configure = fake.configure  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "structlog", module)
    return fake


def test_bind_loza_installs_loza_processor(fake_structlog: _FakeStructlog) -> None:
    from loza.integrations.structlog.adapter import StructlogProcessor, bind_loza

    logger = object()
    existing = object()
    fake_structlog._processors = [existing]

    result = bind_loza(logger)

    # The logger is returned unchanged for chaining/drop-in compatibility...
    assert result is logger
    # ...but the Loza processor is now actually installed, ahead of any existing
    # processors so downstream renderers still receive the original event dict.
    assert len(fake_structlog.configure_calls) == 1
    installed = fake_structlog.configure_calls[0]["processors"]
    assert isinstance(installed, list)
    assert isinstance(installed[0], StructlogProcessor)
    assert installed[1] is existing


def test_bind_loza_is_idempotent(fake_structlog: _FakeStructlog) -> None:
    from loza.integrations.structlog.adapter import StructlogProcessor, bind_loza

    logger = object()
    bind_loza(logger)
    bind_loza(logger)

    # Re-binding must not stack duplicate Loza processors on the chain.
    assert len(fake_structlog.configure_calls) == 1
    installed = fake_structlog.configure_calls[0]["processors"]
    assert sum(isinstance(item, StructlogProcessor) for item in installed) == 1


def test_bind_loza_handles_missing_processor_config(fake_structlog: _FakeStructlog) -> None:
    from loza.integrations.structlog.adapter import StructlogProcessor, bind_loza

    # A configuration without a ``processors`` key should still get a working chain.
    sys.modules["structlog"].get_config = lambda: {}  # type: ignore[attr-defined]

    assert bind_loza(object()) is not None

    installed = fake_structlog.configure_calls[0]["processors"]
    assert isinstance(installed, list)
    assert len(installed) == 1
    assert isinstance(installed[0], StructlogProcessor)
