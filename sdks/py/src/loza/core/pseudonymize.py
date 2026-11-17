from __future__ import annotations

import hashlib
import hmac
import json
import os
import secrets
from typing import Any

__all__ = ["configure_hash_secret", "get_hash_secret", "pseudonymize"]

# Domain separator so digests produced here cannot be confused with a plain
# SHA-256 of the same value (or with another Loza hashing context).
_DOMAIN = b"loza-pseudonym-v1"


def _load_secret() -> bytes:
    """Load the pseudonymization secret from the environment, or generate one.

    A random per-process secret is a safe default: it makes precomputed
    rainbow-table/dictionary recovery of low-entropy values (emails, phone
    numbers, user IDs) impossible. Operators that need pseudonyms to remain
    stable across restarts/services should set ``LOZA_HASH_SALT`` (or
    ``Config.security.hash_salt``) to a high-entropy secret of at least 32
    bytes and keep it out of source control.
    """
    raw = os.environ.get("LOZA_HASH_SALT", "")
    if raw:
        return raw.encode("utf-8")
    return secrets.token_bytes(32)


_secret: bytes = _load_secret()


def configure_hash_secret(secret: str | bytes | None) -> None:
    """Set the process-wide secret used for pseudonymization.

    Passing ``None`` or an empty value restores the random-per-process default.
    """
    global _secret
    if not secret:
        _secret = secrets.token_bytes(32)
    elif isinstance(secret, str):
        _secret = secret.encode("utf-8")
    else:
        _secret = secret


def get_hash_secret() -> bytes:
    """Return the active secret (primarily for tests and diagnostics)."""
    return _secret


def _stable_bytes(value: Any) -> bytes:
    if isinstance(value, (dict, list, tuple)):
        try:
            text = json.dumps(value, sort_keys=True, default=str, separators=(",", ":"))
        except (TypeError, ValueError):
            text = str(value)
    else:
        text = str(value)
    return text.encode("utf-8")


def pseudonymize(value: Any) -> str:
    """Return a keyed HMAC-SHA256 pseudonym of ``value``.

    Unlike a bare SHA-256 digest, the output cannot be recovered from a
    precomputed table unless the attacker also knows the secret salt. The same
    secret always yields the same pseudonym, so events remain correlatable.
    """
    return hmac.new(_secret, _DOMAIN + b"\x00" + _stable_bytes(value), hashlib.sha256).hexdigest()
