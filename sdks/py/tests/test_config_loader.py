import pytest

from loza.config.config import _parse_scalar as parse_scalar_config
from loza.config.config import load_layered_config as load_config_module
from loza.core.config import _parse_scalar as parse_scalar_core
from loza.core.config import load_layered_config as load_core_module


@pytest.mark.parametrize(
    ("raw", "expected"),
    [
        ("true", True),
        ("True", True),
        ("yes", True),
        ("YES", True),
        ("on", True),
        ("On", True),
        ("false", False),
        ("False", False),
        ("no", False),
        ("NO", False),
        ("off", False),
        ("Off", False),
    ],
)
def test_parse_scalar_recognizes_full_boolean_vocabulary(raw, expected):
    assert parse_scalar_config(raw) is expected
    assert parse_scalar_core(raw) is expected


@pytest.mark.parametrize("loader", [load_config_module, load_core_module], ids=["config", "core"])
def test_yaml_no_and_off_do_not_enable_booleans(tmp_path, monkeypatch, loader):
    defaults = tmp_path / "loza-py.defaults.yaml"
    defaults.write_text("service: svc\n", encoding="utf-8")
    user = tmp_path / ".loza-py.yaml"
    user.write_text(
        "\n".join(
            [
                "strict: no",
                "checkpoint_emit_immediately: off",
                "async_config:",
                "  enabled: no",
                "security:",
                "  redact_by_default: no",
                "  allow_pii: off",
                "  drop_oversized_events: no",
            ]
        ),
        encoding="utf-8",
    )
    monkeypatch.setenv("LOZA_PY_DEFAULTS", str(defaults))
    monkeypatch.setenv("LOZA_PY_CONFIG", str(user))

    cfg = loader()

    assert cfg.strict is False
    assert cfg.checkpoint_emit_immediately is False
    assert cfg.async_config.enabled is False
    assert cfg.security.redact_by_default is False
    assert cfg.security.allow_pii is False
    assert cfg.security.drop_oversized_events is False


@pytest.mark.parametrize("loader", [load_config_module, load_core_module], ids=["config", "core"])
def test_yaml_yes_and_on_enable_booleans(tmp_path, monkeypatch, loader):
    defaults = tmp_path / "loza-py.defaults.yaml"
    defaults.write_text("service: svc\n", encoding="utf-8")
    user = tmp_path / ".loza-py.yaml"
    user.write_text(
        "\n".join(
            [
                "strict: yes",
                "checkpoint_emit_immediately: on",
                "async_config:",
                "  enabled: on",
                "security:",
                "  allow_pii: yes",
                "  drop_oversized_events: on",
            ]
        ),
        encoding="utf-8",
    )
    monkeypatch.setenv("LOZA_PY_DEFAULTS", str(defaults))
    monkeypatch.setenv("LOZA_PY_CONFIG", str(user))

    cfg = loader()

    assert cfg.strict is True
    assert cfg.checkpoint_emit_immediately is True
    assert cfg.async_config.enabled is True
    assert cfg.security.allow_pii is True
    assert cfg.security.drop_oversized_events is True


def test_load_layered_config_defaults_then_user(tmp_path, monkeypatch):
    defaults = tmp_path / "loza-py.defaults.yaml"
    user = tmp_path / ".loza-py.yaml"
    defaults.write_text(
        "\n".join(
            [
                "service: default-service",
                "environment: development",
                "level: info",
                "async_config:",
                "  enabled: false",
                "  queue_size: 8192",
                "security:",
                "  max_event_bytes: 262144",
            ]
        ),
        encoding="utf-8",
    )
    user.write_text("service: user-service\nlevel: debug\n", encoding="utf-8")
    monkeypatch.setenv("LOZA_PY_DEFAULTS", str(defaults))
    monkeypatch.setenv("LOZA_PY_CONFIG", str(user))

    cfg = load_core_module()

    assert cfg.service == "user-service"
    assert cfg.level == "debug"
    assert cfg.security.max_event_bytes == 262144
