"""Regression tests: config loaders must use the exported env helpers.

``get_env``/``get_env_bool``/``get_env_int`` are re-exported from
``loza.config`` via ``from .env import *``. While the loaders parsed env vars
inline, changes to those helpers never affected real config loading. These
tests pin the contract that both loaders actually consult the helpers.
"""

from __future__ import annotations

import pytest

import loza.config.config as layered_config
import loza.core.config as legacy_config
from loza.config.env import (
    ENV_ASYNC_ENABLED,
    ENV_BATCH_SIZE,
    ENV_MAX_BUFFER_SIZE,
    ENV_MAX_EVENT_BYTES,
    ENV_SERVICE_NAME,
    ENV_STRICT,
)

LOADERS = [layered_config, legacy_config]


@pytest.mark.parametrize("loader", LOADERS)
def test_apply_env_vars_uses_get_env(monkeypatch: pytest.MonkeyPatch, loader) -> None:
    def fake_get_env(key: str, default: str = "") -> str:
        return "from-helper" if key == ENV_SERVICE_NAME else default

    monkeypatch.setattr(loader, "get_env", fake_get_env)

    applied = loader._apply_env_vars(loader.Config())

    assert applied.service == "from-helper"


@pytest.mark.parametrize("loader", LOADERS)
def test_apply_env_vars_uses_get_env_bool(monkeypatch: pytest.MonkeyPatch, loader) -> None:
    monkeypatch.setattr(loader, "get_env_bool", lambda key, default=False: True)

    applied = loader._apply_env_vars(loader.Config())

    assert applied.strict is True
    assert applied.async_config.enabled is True


@pytest.mark.parametrize("loader", LOADERS)
def test_apply_env_vars_uses_get_env_int(monkeypatch: pytest.MonkeyPatch, loader) -> None:
    monkeypatch.setattr(loader, "get_env_int", lambda key, default=0: 4321)

    applied = loader._apply_env_vars(loader.Config())

    assert applied.async_config.queue_size == 4321
    assert applied.security.max_event_bytes == 4321


def test_layered_loader_uses_helper_for_max_batch_bytes(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(layered_config, "get_env_int", lambda key, default=0: 4321)

    applied = layered_config._apply_env_vars(layered_config.Config())

    assert applied.async_config.max_batch_bytes == 4321


def test_legacy_loader_uses_helper_for_batch_size(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(legacy_config, "get_env_int", lambda key, default=0: 4321)

    applied = legacy_config._apply_env_vars(legacy_config.Config())

    assert applied.async_config.batch_size == 4321


def test_layered_loader_parses_real_env_values(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv(ENV_SERVICE_NAME, "svc")
    monkeypatch.setenv(ENV_STRICT, "yes")
    monkeypatch.setenv(ENV_ASYNC_ENABLED, "true")
    monkeypatch.setenv(ENV_MAX_BUFFER_SIZE, "12")
    monkeypatch.setenv(ENV_BATCH_SIZE, "4")
    monkeypatch.setenv(ENV_MAX_EVENT_BYTES, "1024")

    applied = layered_config._apply_env_vars(layered_config.Config())

    assert applied.service == "svc"
    assert applied.strict is True
    assert applied.async_config.enabled is True
    assert applied.async_config.queue_size == 12
    assert applied.async_config.max_batch_bytes == 4
    assert applied.security.max_event_bytes == 1024
