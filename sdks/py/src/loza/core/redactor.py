from __future__ import annotations

import copy
import json
import re
from collections.abc import Callable
from typing import Any

from .hashing import hash_sensitive_text
from .hashing import secure_hash

REDACTED_VALUE = "[REDACTED]"

Redactor = Callable[[dict[str, Any]], dict[str, Any]]

# Safety-net keys for obviously sensitive fields. The collector owns the real
# PII policy (email, phone, SSN, IP, regex, tenant-specific rules, etc.).
DEFAULT_KEYS = {
    "password",
    "passwd",
    "pwd",
    "secret",
    "token",
    "access_token",
    "refresh_token",
    "api_key",
    "apikey",
    "auth",
    "authorization",
    "credential",
    "private_key",
    "client_secret",
}


def _walk(value: Any, fn: Callable[[str, Any], tuple[bool, Any]]) -> Any:
    if isinstance(value, dict):
        out = {}
        for key, child in value.items():
            keep, new_value = fn(key, child)
            if keep:
                out[key] = _walk(new_value, fn)
        return out
    if isinstance(value, (list, tuple)):
        rebuilt = [_walk(item, fn) for item in value]
        return tuple(rebuilt) if isinstance(value, tuple) else rebuilt
    return value


def _stable_text(value: Any) -> str:
    if isinstance(value, (dict, list, tuple)):
        try:
            return json.dumps(value, sort_keys=True, default=str, separators=(",", ":"))
        except TypeError:
            return str(value)
    return str(value)


def _hash(value: Any) -> str:
    return hash_sensitive_text(_stable_text(value))
    return secure_hash(_stable_text(value))


def _mask(value: Any, prefix: int = 2, suffix: int = 2) -> str:
    text = _stable_text(value)
    if len(text) <= prefix + suffix:
        return "*" * max(4, len(text))
    return text[:prefix] + "*" * (len(text) - prefix - suffix) + text[-suffix:]


def _matches_key(key: str, wanted: set[str]) -> bool:
    """Return True when ``key`` names a sensitive field.

    Field names are frequently compound, and the sensitive term is not always
    the final segment (e.g. ``password_confirmation``, ``secret_value``,
    ``access_token_claims``). Match each wanted term against any whole run of
    tokens delimited by separators, so compound wanted terms such as
    ``api_key`` are also found inside ``user_api_key``.
    """
    lowered = key.lower()
    if lowered in wanted:
        return True
    for term in wanted:
        if not term:
            continue
        pattern = rf"(?:^|[_.-]){re.escape(term)}(?:$|[_.-])"
        if re.search(pattern, lowered):
            return True
    return False


def redact(*keys: str) -> Redactor:
    """Alias for redact_keys."""
    return redact_keys(*keys)


def redact_keys(*keys: str) -> Redactor:
    wanted = {key.lower() for key in keys}
    return lambda payload: _walk(
        copy.deepcopy(payload),
        lambda key, value: (True, REDACTED_VALUE) if _matches_key(key, wanted) else (True, value),
    )


def default_redactor() -> Redactor:
    return redact_keys(*DEFAULT_KEYS)


def noop_redactor() -> Redactor:
    """Return an identity redactor that leaves payloads unchanged."""
    return lambda payload: payload


def hash_keys(*keys: str) -> Redactor:
    wanted = {key.lower() for key in keys}
    return lambda payload: _walk(
        copy.deepcopy(payload),
        lambda key, value: (True, _hash(value)) if _matches_key(key, wanted) else (True, value),
    )


def drop_keys(*keys: str) -> Redactor:
    wanted = {key.lower() for key in keys}
    return lambda payload: _walk(
        copy.deepcopy(payload),
        lambda key, value: (False, value) if _matches_key(key, wanted) else (True, value),
    )


def mask_keys(*keys: str, prefix: int = 2, suffix: int = 2) -> Redactor:
    wanted = {key.lower() for key in keys}
    return lambda payload: _walk(
        copy.deepcopy(payload),
        lambda key, value: (
            (True, _mask(value, prefix=prefix, suffix=suffix)) if _matches_key(key, wanted) else (True, value)
        ),
    )


def redact_patterns(*patterns: str) -> Redactor:
    """Redact string values matching any of the given regex patterns."""
    compiled = [re.compile(p) for p in patterns]

    def _apply(payload: dict[str, Any]) -> dict[str, Any]:
        def _check(key: str, value: Any) -> tuple[bool, Any]:
            if isinstance(value, str):
                for pat in compiled:
                    if pat.search(value):
                        return True, REDACTED_VALUE
            return True, value

        return _walk(copy.deepcopy(payload), _check)

    return _apply


def sensitive_attrs_redactor(sensitive_keys: set[str]) -> Redactor:
    """Create a redactor that redacts keys marked sensitive via Attr.sensitive."""
    if not sensitive_keys:
        return noop_redactor()
    return redact_keys(*sensitive_keys)


def compose_redactors(*redactors: Redactor) -> Redactor:
    def apply(payload: dict[str, Any]) -> dict[str, Any]:
        current = copy.deepcopy(payload)
        for redactor in redactors:
            current = redactor(current)
        return current

    return apply
