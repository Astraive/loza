"""Regression tests for the consolidated duplicate-field helper (issue #813).

``apply_duplicate`` used to exist as dead code in ``core/duplicate.py`` and as
two byte-for-byte copies in the client and logger Loggers.  The logic now lives
in exactly one place (``core.duplicate_policy``) and both Loggers delegate to
it, so a fix made once takes effect everywhere.
"""

from __future__ import annotations

import importlib.util

import pytest

import loza
from loza.core import client as client_module
from loza.core import duplicate_policy
from loza.core import logger as logger_module
from loza.core.client import Logger as LegacyLogger
from loza.core.logger import Logger

_POLICIES = (
    loza.CanonicalWins,
    loza.UserWins,
    loza.FirstWins,
    loza.LastWins,
    loza.KeepBoth,
    loza.ErrorOnDuplicate,
)


def test_legacy_duplicate_module_is_gone() -> None:
    """The dead duplicate.py module must no longer be importable."""
    assert importlib.util.find_spec("loza.core.duplicate") is None


@pytest.mark.parametrize(
    ("policy", "expected"),
    [
        (loza.CanonicalWins, "canonical"),
        (loza.FirstWins, "canonical"),
        (loza.UserWins, "user"),
        (loza.LastWins, "user"),
        (loza.KeepBoth, ["canonical", "user"]),
    ],
)
def test_shared_helper_applies_policy(policy: str, expected: object) -> None:
    target: dict[str, object] = {"event": "canonical"}
    duplicate_policy.apply_duplicate(target, "event", "user", policy)
    assert target["event"] == expected


def test_shared_helper_error_on_duplicate() -> None:
    with pytest.raises(ValueError, match="duplicate field: event"):
        duplicate_policy.apply_duplicate({"event": "canonical"}, "event", "user", loza.ErrorOnDuplicate)


def test_shared_helper_inserts_missing_key_even_for_error_policy() -> None:
    target: dict[str, object] = {}
    duplicate_policy.apply_duplicate(target, "new", 1, loza.ErrorOnDuplicate)
    assert target == {"new": 1}


@pytest.mark.parametrize("policy", _POLICIES)
def test_all_entry_points_agree(policy: str) -> None:
    """The shared helper and both Logger wrappers must behave identically."""
    outcomes: list[tuple[str, object]] = []
    for impl in (
        duplicate_policy.apply_duplicate,
        Logger._apply_duplicate,
        LegacyLogger._apply_duplicate,
    ):
        target: dict[str, object] = {"event": "canonical"}
        try:
            impl(target, "event", "user", policy)
        except ValueError as exc:
            outcomes.append(("error", str(exc)))
        else:
            outcomes.append(("ok", target["event"]))
    assert outcomes[0] == outcomes[1] == outcomes[2]


def test_both_loggers_delegate_to_shared_helper(monkeypatch: pytest.MonkeyPatch) -> None:
    """A change to the single helper must reach both Logger implementations."""
    calls: list[str] = []
    original = duplicate_policy.apply_duplicate

    def spy(target: dict[str, object], key: str, value: object, policy: str) -> None:
        calls.append(policy)
        original(target, key, value, policy)

    monkeypatch.setattr(client_module, "apply_duplicate", spy)
    monkeypatch.setattr(logger_module, "apply_duplicate", spy)

    Logger._apply_duplicate({}, "k", "v", loza.LastWins)
    LegacyLogger._apply_duplicate({}, "k", "v", loza.LastWins)

    assert calls == [loza.LastWins, loza.LastWins]
