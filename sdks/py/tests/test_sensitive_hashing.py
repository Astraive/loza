from __future__ import annotations

import hashlib
import hmac

from loza.core import hashing, redactor, security
from loza.core.event import EventContext, Params, sanitize_event
from loza.core.logger import Logger

PII = "alice@example.com"


def _bare_sha256(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def test_sanitize_event_uses_keyed_hmac_not_bare_sha256(monkeypatch):
    """The reported defect: unsalted SHA-256 of PII was rainbow-table reversible."""
    secret = b"shared-deployment-secret"
    monkeypatch.setenv("LOZA_HASH_SECRET", secret.decode())

    ctx = EventContext(service="svc", params=Params())
    ctx.user["email"] = PII
    ctx._hash_keys.add("user.email")

    safe = sanitize_event(ctx)
    hashed = safe.user["email"]

    assert hashed != PII
    # A plain SHA-256 digest (the vulnerable behavior) must no longer appear.
    assert hashed != _bare_sha256(PII)
    assert hashed == hmac.new(secret, PII.encode("utf-8"), hashlib.sha256).hexdigest()
    # Sanitization still leaves the original event untouched.
    assert ctx.user["email"] == PII


def test_hash_is_stable_within_deployment_but_key_dependent(monkeypatch):
    secret_a = b"deployment-a-secret"
    secret_b = b"deployment-b-secret"

    monkeypatch.setattr(hashing, "_SECRET", secret_a)
    first = hashing.hash_sensitive_value(PII)
    second = hashing.hash_sensitive_value(PII)
    assert first == second == hmac.new(secret_a, PII.encode("utf-8"), hashlib.sha256).hexdigest()

    monkeypatch.setattr(hashing, "_SECRET", secret_b)
    rotated = hashing.hash_sensitive_value(PII)
    assert rotated != first
    assert rotated == hmac.new(secret_b, PII.encode("utf-8"), hashlib.sha256).hexdigest()


def test_hash_secret_configuration(monkeypatch):
    monkeypatch.setenv("LOZA_HASH_SECRET", "per-deployment-secret")
    assert hashing._load_hash_secret() == b"per-deployment-secret"

    monkeypatch.delenv("LOZA_HASH_SECRET", raising=False)
    generated = hashing._load_hash_secret()
    assert isinstance(generated, bytes) and len(generated) >= 32


def test_adjacent_sanitizers_share_keyed_hash(monkeypatch):
    """The same unsalted pattern existed in the logger/redactor/security helpers."""
    secret = b"shared-deployment-secret"
    monkeypatch.setenv("LOZA_HASH_SECRET", secret.decode())
    expected = hmac.new(secret, PII.encode("utf-8"), hashlib.sha256).hexdigest()

    target = {"email": PII}
    Logger._hash_at_path(target, "email")
    assert target["email"] == expected

    assert security.hash_value(PII) == expected
    assert redactor.hash_keys("email")({"email": PII})["email"] == expected

    for digest in (target["email"], security.hash_value(PII)):
        assert digest != _bare_sha256(PII)
