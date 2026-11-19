"""Regression tests for get_env_int integer parsing from environment variables."""

import pytest

from loza.config.env import ENV_MAX_BUFFER_SIZE, get_env_int


@pytest.mark.parametrize(
    ("raw", "default", "expected"),
    [
        ("-1", 0, -1),  # valid negative integers must not be discarded
        ("+5", 0, 5),  # explicit sign is accepted by int()
        ("42", 0, 42),  # ordinary positive integers still parse
        ("²", 99, 99),  # isdigit() is True but int() raises -> fall back
        ("-²", 99, 99),  # signed non-decimal digit must not raise either
        ("abc", 99, 99),  # non-numeric values fall back to the default
        ("", 7, 7),  # missing/empty values fall back to the default
    ],
)
def test_get_env_int_parses_like_int_with_safe_default(
    monkeypatch: pytest.MonkeyPatch, raw: str, default: int, expected: int
) -> None:
    monkeypatch.setenv(ENV_MAX_BUFFER_SIZE, raw)

    assert get_env_int(ENV_MAX_BUFFER_SIZE, default=default) == expected


def test_get_env_int_returns_default_when_variable_is_unset(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv(ENV_MAX_BUFFER_SIZE, raising=False)

    assert get_env_int(ENV_MAX_BUFFER_SIZE, default=13) == 13
