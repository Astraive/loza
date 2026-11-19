from __future__ import annotations

from pathlib import Path

import pytest

from loza.config import config as legacy_config
from loza.core import config as core_config


@pytest.fixture()
def isolated_file_config(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    """Keep file config out of the way so only env and code layers matter."""
    defaults = tmp_path / "defaults.yaml"
    defaults.write_text("service: file-service\nlevel: info\n", encoding="utf-8")
    monkeypatch.setenv("LOZA_PY_DEFAULTS", str(defaults))
    monkeypatch.setenv("LOZA_PY_CONFIG", str(tmp_path / "missing-user-config.yaml"))
    for key in (
        "LOZA_SERVICE",
        "LOZA_SERVICE_NAME",
        "LOZA_ENVIRONMENT",
        "LOZA_DUPLICATE_POLICY",
        "LOZA_STRICT",
        "LOZA_ASYNC_ENABLED",
    ):
        monkeypatch.delenv(key, raising=False)


@pytest.mark.parametrize("module", [core_config, legacy_config], ids=["core", "legacy"])
def test_explicit_false_code_config_disables_env_and_file_flags(module, isolated_file_config, monkeypatch) -> None:
    """Code config is highest precedence: explicit False must override True from lower layers."""
    monkeypatch.setenv("LOZA_STRICT", "true")
    monkeypatch.setenv("LOZA_ASYNC_ENABLED", "true")

    cfg = module.Config(
        service="svc",
        strict=False,
        checkpoint_emit_immediately=False,
        duplicate_policy=module.CanonicalWins,
        async_config=module.AsyncConfig(enabled=False),
    )

    logger = module.new_client(cfg)
    effective = logger._config

    assert effective.strict is False
    assert effective.checkpoint_emit_immediately is False
    assert effective.async_config.enabled is False
    assert effective.duplicate_policy == module.CanonicalWins


@pytest.mark.parametrize("module", [core_config, legacy_config], ids=["core", "legacy"])
def test_unset_code_config_does_not_override_lower_layers(module, isolated_file_config, monkeypatch) -> None:
    """Fields left unspecified in code config must not clobber lower-layer values."""
    monkeypatch.setenv("LOZA_STRICT", "true")
    monkeypatch.setenv("LOZA_ASYNC_ENABLED", "true")

    logger = module.new_client(module.Config(service="svc"))
    effective = logger._config

    assert effective.strict is True
    assert effective.async_config.enabled is True


@pytest.mark.parametrize("module", [core_config, legacy_config], ids=["core", "legacy"])
def test_merge_code_config_honours_explicit_false_and_default_reset(module) -> None:
    """Direct merge test for both booleans and the duplicate-policy default reset."""
    base = module.Config()
    base.strict = True
    base.checkpoint_emit_immediately = True
    base.async_config.enabled = True
    base.duplicate_policy = module.LastWins

    merged = module._merge_code_config(
        base,
        module.Config(
            strict=False,
            checkpoint_emit_immediately=False,
            duplicate_policy=module.CanonicalWins,
            async_config=module.AsyncConfig(enabled=False),
        ),
    )

    assert merged.strict is False
    assert merged.checkpoint_emit_immediately is False
    assert merged.async_config.enabled is False
    assert merged.duplicate_policy == module.CanonicalWins


@pytest.mark.parametrize("module", [core_config, legacy_config], ids=["core", "legacy"])
def test_merge_code_config_leaves_unset_fields_untouched(module) -> None:
    base = module.Config()
    base.strict = True
    base.checkpoint_emit_immediately = True
    base.async_config.enabled = True
    base.duplicate_policy = module.LastWins

    merged = module._merge_code_config(base, module.Config())

    assert merged.strict is True
    assert merged.checkpoint_emit_immediately is True
    assert merged.async_config.enabled is True
    assert merged.duplicate_policy == module.LastWins
