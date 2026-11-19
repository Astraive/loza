from __future__ import annotations

from typing import Any

CanonicalWins = "canonical_wins"
UserWins = "user_wins"
AttrsWin = UserWins
FirstWins = "first_wins"
LastWins = "last_wins"
KeepBoth = "keep_both"
ErrorOnDuplicate = "error_on_duplicate"


def apply_duplicate(target: dict[str, Any], key: str, value: Any, policy: str) -> None:
    """Apply ``policy`` to ``key`` in ``target``.

    Single source of truth for duplicate-field handling. Both Logger
    implementations import and delegate to this helper so the policy logic
    exists in exactly one place.
    """
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
    target[key] = value
