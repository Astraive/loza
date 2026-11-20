from __future__ import annotations

import hashlib
import hmac
import os
import secrets
from typing import Any

# Optional per-deployment secret. When set, hashed PII stays stable across
# restarts of a deployment (allowable correlation) while still being impossible
# to recover with rainbow/dictionary tables of common low-entropy values.
_HASH_SECRET_ENV = "LOZA_HASH_SECRET"


def _load_hash_secret() -> bytes:
    configured = os.getenv(_HASH_SECRET_ENV, "").strip()
    if configured:
        return configured.encode("utf-8")
    # No deployment secret configured: use an unpredictable per-process key.
    return secrets.token_bytes(32)


_SECRET: bytes = _load_hash_secret()


def hash_sensitive_text(text: str) -> str:
    """Return a keyed HMAC-SHA256 digest of ``text``.

    Unlike a bare SHA-256, the keyed digest cannot be brute-forced with
    precomputed tables of emails, phone numbers, IDs, or other low-entropy PII.
    """
    return hmac.new(_SECRET, text.encode("utf-8"), hashlib.sha256).hexdigest()


def hash_sensitive_value(value: object) -> str:
    """Keyed hash of an arbitrary sensitive value (stringified as before)."""
    return hash_sensitive_text(str(value))


__all__ = ["hash_sensitive_text", "hash_sensitive_value", "HASH_SECRET_ENV", "secure_hash"]

# A secret key makes value hashes non-reversible for low-entropy inputs such as
# emails, phone numbers and account IDs. A keyed HMAC cannot be attacked with
# generic rainbow tables: computing a candidate digest requires the key.
#
# Set LOZA_HASH_SECRET to a stable value shared by every producer when hashes
# must be comparable across processes or restarts. Without it, a random key is
# generated once per process, so hashes are only stable within that process.
HASH_SECRET_ENV = "LOZA_HASH_SECRET"

_process_hash_key: bytes = secrets.token_bytes(32)


def _hash_key() -> bytes:
    configured = os.environ.get(HASH_SECRET_ENV, "")
    if configured:
        return configured.encode("utf-8")
    return _process_hash_key


def secure_hash(value: Any) -> str:
    """Return a keyed HMAC-SHA256 hex digest of ``value``.

    Unlike plain SHA-256, the secret key prevents dictionary and rainbow-table
    recovery of low-entropy PII. The key is read from ``LOZA_HASH_SECRET`` on
    every call, falling back to a per-process random key when unset.
    """
    return hmac.new(_hash_key(), str(value).encode("utf-8"), hashlib.sha256).hexdigest()
