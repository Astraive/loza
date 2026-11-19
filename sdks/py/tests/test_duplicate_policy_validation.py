"""Regression tests for unknown duplicate-policy values (issue #814).

An unrecognized policy must never silently overwrite an existing value; it
should surface a configuration error instead. The policy logic now lives in
``core.duplicate_policy`` and both Logger implementations delegate to it, so
these checks cover every entry point.
"""

from __future__ import annotations

import pytest

from loza.core import duplicate_policy
from loza.core.client import Logger as LegacyLogger
from loza.core.duplicate_policy import (
    CanonicalWins,
    ErrorOnDuplicate,
    FirstWins,
    KeepBoth,
    LastWins,
    UserWins,
)
from loza.core.logger import Logger

# A realistic typo of the supported ``last_wins`` policy.
UNKNOWN_POLICY = "last_win"


def test_unknown_policy_does_not_overwrite_existing_value() -> None:
    target = {"k": "original"}

    with pytest.raises(ValueError, match="unknown duplicate policy"):
        duplicate_policy.apply_duplicate(target, "k", "replacement", UNKNOWN_POLICY)

    assert target == {"k": "original"}


def test_unknown_policy_is_rejected_even_for_a_new_key() -> None:
    target: dict[str, str] = {}

    with pytest.raises(ValueError, match="unknown duplicate policy"):
        duplicate_policy.apply_duplicate(target, "k", "value", UNKNOWN_POLICY)

    assert target == {}


def test_unknown_policy_error_names_the_offending_value() -> None:
    with pytest.raises(ValueError) as exc:
        duplicate_policy.apply_duplicate({"k": "original"}, "k", "replacement", UNKNOWN_POLICY)

    assert UNKNOWN_POLICY in str(exc.value)


@pytest.mark.parametrize(
    ("policy", "expected"),
    [
        (UserWins, "replacement"),
        (LastWins, "replacement"),
        (CanonicalWins, "original"),
        (FirstWins, "original"),
        (KeepBoth, ["original", "replacement"]),
    ],
)
def test_known_policies_keep_their_behavior(policy: str, expected: object) -> None:
    target: dict[str, object] = {"k": "original"}
    duplicate_policy.apply_duplicate(target, "k", "replacement", policy)
    assert target == {"k": expected}


def test_error_on_duplicate_still_raises() -> None:
    with pytest.raises(ValueError, match="duplicate field"):
        duplicate_policy.apply_duplicate({"k": "original"}, "k", "replacement", ErrorOnDuplicate)


@pytest.mark.parametrize("logger_cls", [LegacyLogger, Logger])
def test_both_logger_entry_points_reject_unknown_policy(logger_cls: type) -> None:
    target = {"k": "original"}

    with pytest.raises(ValueError, match="unknown duplicate policy"):
        logger_cls._apply_duplicate(target, "k", "replacement", UNKNOWN_POLICY)

    assert target == {"k": "original"}
