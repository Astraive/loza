from __future__ import annotations

import hashlib
import hmac
import os
import secrets

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


__all__ = ["hash_sensitive_text", "hash_sensitive_value"]
