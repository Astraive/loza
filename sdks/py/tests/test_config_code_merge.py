"""Regression tests for code-layer config precedence in ``_merge_code_config``.

The code layer is the highest-precedence configuration layer, so every non-default
value supplied by the caller must reach the resulting ``Config``. Nested config
objects are merged field-by-field so lower-precedence (file/env) values survive for
sub-fields the caller left at their defaults.
"""

from __future__ import annotations

from pathlib import Path

import pytest

import loza
from loza.core import config as config_module


@pytest.fixture(autouse=True)
def _isolated_config_layers(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    """Keep file/env config layers out of these tests for deterministic results."""
    monkeypatch.chdir(tmp_path)
    monkeypatch.setenv("LOZA_PY_DEFAULTS", str(tmp_path / "missing-defaults.yaml"))
    monkeypatch.setenv("LOZA_PY_CONFIG", str(tmp_path / "missing-user.yaml"))
    for key in (
        "LOZA_DSN",
        "LOZA_COLLECTOR_URL",
        "LOZA_COLLECTOR_ENDPOINT",
        "LOZA_SERVICE",
        "LOZA_SERVICE_NAME",
        "LOZA_SERVICE_VERSION",
        "LOZA_ENVIRONMENT",
        "LOZA_REGION",
        "LOZA_LOG_LEVEL",
        "LOZA_API_KEY",
        "LOZA_DUPLICATE_POLICY",
        "LOZA_STRICT",
        "LOZA_ASYNC_ENABLED",
        "LOZA_MAX_BUFFER_SIZE",
        "LOZA_BATCH_SIZE",
        "LOZA_MAX_EVENT_BYTES",
    ):
        monkeypatch.delenv(key, raising=False)


def test_merge_code_config_applies_all_non_default_fields() -> None:
    mapper = lambda name: name.upper()  # noqa: E731
    code = loza.Config(
        service="svc",
        alias="alias",
        deployment_id="dep-42",
        include_host=True,
        max_checkpoints=7,
        panic_recovery=True,
        exit_on_fatal=True,
        release="r1",
        namespace="ns",
        otel_bridge=True,
        retry=True,
        timeout=2.5,
        logger="my-logger",
        security=loza.SecurityConfig(
            redact_by_default=False,
            allow_pii=True,
            max_field_bytes=128,
            max_event_bytes=1024,
            max_attr_count=16,
            drop_oversized_events=False,
        ),
        field_naming=loza.FieldNamingConfig(policy="preserve_dot_keys", custom_mapper=mapper),
        async_config=loza.AsyncConfig(
            enabled=True,
            queue_size=32,
            workers=4,
            max_batch_bytes=1234,
            flush_interval_ms=77,
            batch_size=9,
        ),
    )

    merged = config_module._merge_code_config(loza.Config(), code)

    assert merged.service == "svc"
    assert merged.alias == "alias"
    assert merged.deployment_id == "dep-42"
    assert merged.include_host is True
    assert merged.max_checkpoints == 7
    assert merged.panic_recovery is True
    assert merged.exit_on_fatal is True
    assert merged.release == "r1"
    assert merged.namespace == "ns"
    assert merged.otel_bridge is True
    assert merged.retry is True
    assert merged.timeout == 2.5
    assert merged.logger == "my-logger"
    assert merged.security == loza.SecurityConfig(
        redact_by_default=False,
        allow_pii=True,
        max_field_bytes=128,
        max_event_bytes=1024,
        max_attr_count=16,
        drop_oversized_events=False,
    )
    assert merged.field_naming.policy == "preserve_dot_keys"
    assert merged.field_naming.custom_mapper is mapper
    assert merged.async_config == loza.AsyncConfig(
        enabled=True,
        queue_size=32,
        workers=4,
        max_batch_bytes=1234,
        flush_interval_ms=77,
        batch_size=9,
    )


def test_merge_code_config_merges_nested_objects_field_by_field() -> None:
    """A caller overriding one sub-field must not wipe a lower-layer value."""
    base = loza.Config()
    base.security.max_event_bytes = 2048  # e.g. supplied by the env layer
    base.async_config.queue_size = 12  # e.g. supplied by the env layer

    code = loza.Config(
        security=loza.SecurityConfig(max_field_bytes=64),
        async_config=loza.AsyncConfig(batch_size=5),
    )

    merged = config_module._merge_code_config(base, code)

    assert merged.security.max_field_bytes == 64
    assert merged.security.max_event_bytes == 2048
    assert merged.async_config.batch_size == 5
    assert merged.async_config.queue_size == 12


def test_new_client_honors_code_security_settings() -> None:
    """The exact reported reproduction: hardening via code must not be dropped."""
    logger = loza.new_client(loza.Config(security=loza.SecurityConfig(max_event_bytes=1024)))
    try:
        assert logger._config.security.max_event_bytes == 1024
    finally:
        logger.shutdown()


def test_legacy_merge_code_config_applies_nested_settings() -> None:
    """The adjacent legacy duplicate had the same field-dropping defect."""
    from loza.config import config as legacy_config

    code = legacy_config.Config(
        security=legacy_config.SecurityConfig(max_event_bytes=1024, allow_pii=True),
        field_naming=legacy_config.FieldNamingConfig(policy="preserve_dot_keys"),
        async_config=legacy_config.AsyncConfig(max_batch_bytes=1234),
        deployment_id="dep-42",
        max_checkpoints=7,
    )

    merged = legacy_config._merge_code_config(legacy_config.Config(), code)

    assert merged.security.max_event_bytes == 1024
    assert merged.security.allow_pii is True
    assert merged.field_naming.policy == "preserve_dot_keys"
    assert merged.async_config.max_batch_bytes == 1234
    assert merged.deployment_id == "dep-42"
    assert merged.max_checkpoints == 7
