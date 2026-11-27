from __future__ import annotations

from typing import Any

CanonicalWins = "canonical_wins"
UserWins = "user_wins"
AttrsWin = UserWins
FirstWins = "first_wins"
LastWins = "last_wins"
KeepBoth = "keep_both"
ErrorOnDuplicate = "error_on_duplicate"

# All recognized duplicate-policy values. AttrsWin is an alias of UserWins, so
# it is already represented here by that shared value.
VALID_POLICIES = frozenset(
    {
        CanonicalWins,
        UserWins,
        FirstWins,
        LastWins,
        KeepBoth,
        ErrorOnDuplicate,
    }
)


def validate_policy(policy: str) -> None:
    """Raise ``ValueError`` when *policy* is not a recognized duplicate policy.

    Without this check an unknown policy (e.g. a typo such as ``last_win``)
    falls through the policy branches and silently overwrites an existing
    value instead of reporting the configuration error.
    """
    if policy not in VALID_POLICIES:
        raise ValueError(f"unknown duplicate policy: {policy!r}; expected one of {sorted(VALID_POLICIES)}")


def apply_duplicate(target: dict[str, Any], key: str, value: Any, policy: str) -> None:
    """Apply ``policy`` to ``key`` in ``target``.

    Single source of truth for duplicate-field handling. Both Logger
    implementations import and delegate to this helper so the policy logic
    exists in exactly one place.
    """
    validate_policy(policy)
    if key not in target:
        target[key] = value
        return
    if policy in {CanonicalWins, FirstWins}:
        return
    if policy in {UserWins, LastWins}:
        target[key] = value
        return
    if policy == KeepBoth:
        old = target[key]
        target[key] = old + [value] if isinstance(old, list) else [old, value]
        return
    if policy == ErrorOnDuplicate:
        raise ValueError(f"duplicate field: {key}")
