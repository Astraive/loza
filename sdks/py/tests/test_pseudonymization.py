from __future__ import annotations

import hashlib
import hmac

import pytest

import loza
from loza.core import config as config_module
from loza.core import event as event_module
from loza.core import redactor
from loza.core import security
from loza.core.pseudonymize import configure_hash_secret, get_hash_secret, pseudonymize

SECRET = "integration-test-secret-with-at-least-32-bytes"


@pytest.fixture(autouse=True)
def _restore_secret():
    original = get_hash_secret()
    try:
        yield
    finally:
        configure_hash_secret(original)


def test_hash_value_is_keyed_not_plain_sha256():
    """MASS-0667: the reported defect was an unsalted, fast SHA-256."""
    configure_hash_secret(SECRET)
    value = "user@example.com"

    digest = security.hash_value(value)
    expected = hmac.new(SECRET.encode("utf-8"), b"loza-pseudonym-v1\x00" + value.encode(), hashlib.sha256).hexdigest()

    assert len(digest) == 64
    assert digest == expected
    assert digest != hashlib.sha256(value.encode("utf-8")).hexdigest()


def test_hash_value_resists_dictionary_recovery_but_stays_correlatable():
    """A rainbow table cannot cover an unknown key, but the same key is stable."""
    value = "555-0100"

    configure_hash_secret(SECRET)
    first = security.hash_value(value)
    configure_hash_secret(SECRET)
    assert security.hash_value(value) == first  # correlation preserved

    configure_hash_secret("a-completely-different-32-byte-secret!")
    second = security.hash_value(value)
    assert second != first  # cannot precompute without the secret
    assert len(second) == 64


def test_hash_string_uses_keyed_pseudonym_and_does_not_embed_raw_value():
    configure_hash_secret(SECRET)
    attr = security.hash_string("user.email", "user@example.com")

    assert attr.hash_value is True
    assert attr.value == pseudonymize("user@example.com")
    assert "user@example.com" not in attr.value


def test_event_hash_path_uses_keyed_pseudonym():
    configure_hash_secret(SECRET)
    ctx = loza.EventContext(service="test", params=loza.Params(event="test.event"))
    ctx.user = {"id": "user-123"}
    ctx._hash_keys.add("user.id")

    safe = event_module.sanitize_event(ctx)

    assert safe.user["id"] == pseudonymize("user-123")
    assert safe is not ctx
    assert ctx.user["id"] == "user-123"  # original untouched


def test_logger_hash_path_uses_keyed_pseudonym():
    configure_hash_secret(SECRET)
    from loza.core.logger import Logger

    target = {"email": "user@example.com"}
    Logger._hash_at_path(target, "email")

    assert target["email"] == pseudonymize("user@example.com")
    assert target["email"] != hashlib.sha256(b"user@example.com").hexdigest()


def test_redactor_hash_keys_uses_keyed_pseudonym():
    configure_hash_secret(SECRET)
    payload = {"nested": {"token": "low-entropy"}}

    result = redactor.hash_keys("token")(payload)

    assert result["nested"]["token"] == pseudonymize("low-entropy")
    assert result["nested"]["token"] != hashlib.sha256(b"low-entropy").hexdigest()
    assert payload["nested"]["token"] == "low-entropy"  # original untouched


def test_email_and_ip_helpers_use_keyed_pseudonym_with_normalization():
    configure_hash_secret(SECRET)

    assert loza.email_hash(" User@Example.COM ").value == pseudonymize("user@example.com")
    assert loza.ip_hash(" 192.168.1.1 ").value == pseudonymize("192.168.1.1")


def test_hash_salt_is_hidden_from_config_repr():
    cfg = loza.SecurityConfig(hash_salt=SECRET)

    assert "hash_salt" not in repr(cfg)
    assert SECRET not in repr(cfg)


def test_config_hash_salt_is_applied_by_new_client(monkeypatch):
    monkeypatch.delenv("LOZA_HASH_SALT", raising=False)
    client = config_module.new_client(
        loza.Config(service="hash-test", environment="test", security=loza.SecurityConfig(hash_salt=SECRET))
    )

    assert client is not None
    assert get_hash_secret() == SECRET.encode("utf-8")
