"""Regression tests for config merge helpers dropping nested/security fields."""

from __future__ import annotations

import loza
from loza.config import config as legacy_config
from loza.core import config as core_config


SECURITY_SETTINGS = {
    "allow_pii": True,
    "redact_by_default": False,
    "max_field_bytes": 1234,
    "max_event_bytes": 4096,
    "max_attr_count": 64,
}
NAMING_SETTINGS = {"policy": "preserve_dot_keys"}


def test_legacy_file_config_keeps_security_and_naming() -> None:
    file_cfg = legacy_config._config_from_mapping(
        {
            "service": "svc",
            "security": SECURITY_SETTINGS,
            "field_naming": NAMING_SETTINGS,
            "async_config": {"max_batch_bytes": 4096},
            "deployment_id": "dep-1",
            "include_host": True,
            "max_checkpoints": 7,
            "panic_recovery": True,
        }
    )

    merged = legacy_config._merge_file_config(legacy_config.Config(), file_cfg)

    assert merged.security.allow_pii is True
    assert merged.security.redact_by_default is False
    assert merged.security.max_field_bytes == 1234
    assert merged.security.max_event_bytes == 4096
    assert merged.security.max_attr_count == 64
    assert merged.field_naming.policy == "preserve_dot_keys"
    assert merged.async_config.max_batch_bytes == 4096
    assert merged.deployment_id == "dep-1"
    assert merged.include_host is True
    assert merged.max_checkpoints == 7
    assert merged.panic_recovery is True


def test_legacy_code_config_keeps_security_and_naming() -> None:
    code = legacy_config.Config(
        service="svc",
        security=legacy_config.SecurityConfig(**SECURITY_SETTINGS),
        field_naming=legacy_config.FieldNamingConfig(policy="preserve_dot_keys"),
        async_config=legacy_config.AsyncConfig(max_batch_bytes=4096),
        deployment_id="dep-1",
        include_host=True,
        max_checkpoints=7,
        panic_recovery=True,
    )

    merged = legacy_config._merge_code_config(legacy_config.Config(), code)

    assert merged.security.allow_pii is True
    assert merged.security.redact_by_default is False
    assert merged.security.max_field_bytes == 1234
    assert merged.security.max_event_bytes == 4096
    assert merged.security.max_attr_count == 64
    assert merged.field_naming.policy == "preserve_dot_keys"
    assert merged.async_config.max_batch_bytes == 4096
    assert merged.deployment_id == "dep-1"
    assert merged.include_host is True
    assert merged.max_checkpoints == 7
    assert merged.panic_recovery is True


def test_core_merge_keeps_security_and_naming() -> None:
    file_cfg = core_config._config_from_mapping(
        {
            "service": "svc",
            "security": SECURITY_SETTINGS,
            "field_naming": NAMING_SETTINGS,
            "async_config": {"max_batch_bytes": 4096, "batch_size": 7},
            "deployment_id": "dep-1",
            "include_host": True,
            "max_checkpoints": 7,
            "panic_recovery": True,
        }
    )

    merged = core_config._merge_file_config(core_config.Config(), file_cfg)

    code = core_config.Config(
        service="svc",
        security=core_config.SecurityConfig(**SECURITY_SETTINGS),
        field_naming=core_config.FieldNamingConfig(policy="preserve_dot_keys"),
        async_config=core_config.AsyncConfig(max_batch_bytes=4096, batch_size=7),
        deployment_id="dep-1",
        include_host=True,
        max_checkpoints=7,
        panic_recovery=True,
    )
    merged = core_config._merge_code_config(merged, code)

    assert merged.security.allow_pii is True
    assert merged.security.redact_by_default is False
    assert merged.security.max_field_bytes == 1234
    assert merged.security.max_event_bytes == 4096
    assert merged.security.max_attr_count == 64
    assert merged.field_naming.policy == "preserve_dot_keys"
    assert merged.async_config.max_batch_bytes == 4096
    assert merged.async_config.batch_size == 7
    assert merged.deployment_id == "dep-1"
    assert merged.include_host is True
    assert merged.max_checkpoints == 7
    assert merged.panic_recovery is True


def test_new_client_applies_file_security_settings(tmp_path, monkeypatch) -> None:
    defaults = tmp_path / "loza-py.defaults.yaml"
    defaults.write_text(
        "\n".join(
            [
                "service: file-svc",
                "security:",
                "  allow_pii: true",
                "  max_field_bytes: 1234",
                "field_naming:",
                "  policy: preserve_dot_keys",
                "deployment_id: dep-1",
                "panic_recovery: true",
            ]
        ),
        encoding="utf-8",
    )
    monkeypatch.setenv("LOZA_PY_DEFAULTS", str(defaults))
    monkeypatch.delenv("LOZA_PY_CONFIG", raising=False)
    for env_key in (
        "LOZA_SERVICE",
        "LOZA_SERVICE_NAME",
        "LOZA_MAX_EVENT_BYTES",
        "LOZA_BATCH_SIZE",
        "LOZA_MAX_BUFFER_SIZE",
        "LOZA_ASYNC_ENABLED",
    ):
        monkeypatch.delenv(env_key, raising=False)

    logger = legacy_config.new_client(legacy_config.Config(environment="test"))
    cfg = logger._config

    assert cfg.security.allow_pii is True
    assert cfg.security.max_field_bytes == 1234
    assert cfg.field_naming.policy == "preserve_dot_keys"
    assert cfg.deployment_id == "dep-1"
    assert cfg.panic_recovery is True


def test_public_new_client_applies_file_security_settings(tmp_path, monkeypatch) -> None:
    defaults = tmp_path / "loza-py.defaults.yaml"
    defaults.write_text(
        "\n".join(
            [
                "service: public-svc",
                "security:",
                "  allow_pii: true",
                "  max_field_bytes: 777",
                "field_naming:",
                "  policy: preserve_dot_keys",
                "panic_recovery: true",
                "max_checkpoints: 9",
            ]
        ),
        encoding="utf-8",
    )
    monkeypatch.setenv("LOZA_PY_DEFAULTS", str(defaults))
    monkeypatch.delenv("LOZA_PY_CONFIG", raising=False)
    for env_key in ("LOZA_MAX_EVENT_BYTES", "LOZA_BATCH_SIZE", "LOZA_MAX_BUFFER_SIZE", "LOZA_ASYNC_ENABLED"):
        monkeypatch.delenv(env_key, raising=False)

    logger = loza.new_client(loza.Config(environment="test"))
    cfg = logger._config

    assert cfg.security.allow_pii is True
    assert cfg.security.max_field_bytes == 777
    assert cfg.field_naming.policy == "preserve_dot_keys"
    assert cfg.panic_recovery is True
    assert cfg.max_checkpoints == 9
