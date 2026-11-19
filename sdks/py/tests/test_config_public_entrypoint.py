"""Regression tests for the public config entry points (issue #798).

The exported loader must run the same full, validated pipeline as ``new_client``:
defaults -> file -> env vars -> code config -> validate(). Previously the exported
``load_layered_config`` returned an unvalidated config that ignored environment
variables, silently diverging from ``new_client``.
"""

import pytest

from loza.config import Config, load_layered_config, new_client


ENV_KEYS = (
    "LOZA_SERVICE",
    "LOZA_SERVICE_NAME",
    "LOZA_SERVICE_VERSION",
    "LOZA_ENVIRONMENT",
    "LOZA_REGION",
    "LOZA_LOG_LEVEL",
    "LOZA_COLLECTOR_URL",
    "LOZA_COLLECTOR_ENDPOINT",
    "LOZA_DUPLICATE_POLICY",
    "LOZA_STRICT",
    "LOZA_ASYNC_ENABLED",
    "LOZA_MAX_BUFFER_SIZE",
    "LOZA_BATCH_SIZE",
    "LOZA_MAX_EVENT_BYTES",
)


@pytest.fixture(autouse=True)
def _isolated_config(tmp_path, monkeypatch):
    """Clear ambient LOZA env vars and point config paths at a temp dir."""
    for key in ENV_KEYS:
        monkeypatch.delenv(key, raising=False)
    defaults = tmp_path / "loza-py.defaults.yaml"
    defaults.write_text("service: file-svc\nlevel: info\nstrict: false\n", encoding="utf-8")
    monkeypatch.setenv("LOZA_PY_DEFAULTS", str(defaults))
    monkeypatch.delenv("LOZA_PY_CONFIG", raising=False)
    return defaults


def test_load_layered_config_applies_env_vars(monkeypatch):
    """The exported loader must honour env vars like new_client does."""
    monkeypatch.setenv("LOZA_LOG_LEVEL", "error")
    monkeypatch.setenv("LOZA_SERVICE", "env-svc")

    cfg = load_layered_config()

    assert cfg.level == "error"
    assert cfg.service == "env-svc"


def test_load_layered_config_validates_result(tmp_path, monkeypatch):
    """The exported loader must validate instead of returning bad config silently."""
    monkeypatch.setenv("LOZA_LOG_LEVEL", "not-a-level")
    with pytest.raises(ValueError, match="unsupported level"):
        load_layered_config()

    # strict mode with no service must be rejected
    no_service = tmp_path / "no-service.yaml"
    no_service.write_text("service: ''\nlevel: info\nstrict: false\n", encoding="utf-8")
    monkeypatch.setenv("LOZA_PY_DEFAULTS", str(no_service))
    monkeypatch.setenv("LOZA_LOG_LEVEL", "info")
    monkeypatch.setenv("LOZA_STRICT", "true")
    monkeypatch.setenv("LOZA_SERVICE", "")
    with pytest.raises(ValueError, match="strict mode requires service"):
        load_layered_config()


def test_new_client_is_exported_full_pipeline(monkeypatch):
    """new_client is the public entry point and runs env + code precedence + validate."""
    monkeypatch.setenv("LOZA_LOG_LEVEL", "warn")

    client = new_client(Config(level="fatal"))

    # env var wins over file, code config wins over env.
    assert client._config.level == "fatal"

    with pytest.raises(ValueError, match="unsupported level"):
        new_client(Config(level="bogus"))
