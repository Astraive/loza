from __future__ import annotations

import hashlib
import hmac
import os
import subprocess
import sys
from pathlib import Path

import pytest

import loza
from loza.core import attr as attr_module

SECRET = b"pii-hash-regression-secret"
OTHER_SECRET = b"a-different-pii-hash-secret"


def _expected(value: str, secret: bytes) -> str:
    return hmac.new(secret, value.encode("utf-8"), hashlib.sha256).hexdigest()


def test_email_hash_is_keyed_and_normalizes(monkeypatch):
    monkeypatch.setenv("LOZA_PII_HASH_SECRET", "env-should-be-overridden")
    attr = loza.email_hash("  User@Example.COM  ", secret=SECRET)
    assert attr.key == "email.hash"
    assert attr.value == _expected("user@example.com", SECRET)
    assert attr.value != hashlib.sha256(b"user@example.com").hexdigest()


def test_ip_hash_is_keyed_and_trims(monkeypatch):
    monkeypatch.setenv("LOZA_PII_HASH_SECRET", "env-should-be-overridden")
    attr = loza.ip_hash("  192.168.1.1  ", secret=SECRET)
    assert attr.key == "ip.hash"
    assert attr.value == _expected("192.168.1.1", SECRET)
    assert attr.value != hashlib.sha256(b"192.168.1.1").hexdigest()


def test_different_secrets_produce_different_digests():
    assert loza.email_hash("user@example.com", secret=SECRET).value != loza.email_hash(
        "user@example.com", secret=OTHER_SECRET
    ).value
    assert loza.ip_hash("192.168.1.1", secret=SECRET).value != loza.ip_hash(
        "192.168.1.1", secret=OTHER_SECRET
    ).value


def test_env_secret_used_without_explicit_secret(monkeypatch):
    monkeypatch.setenv("LOZA_PII_HASH_SECRET", "from-environment")
    assert loza.email_hash("User@Example.com").value == _expected("user@example.com", b"from-environment")
    assert loza.ip_hash("10.0.0.1").value == _expected("10.0.0.1", b"from-environment")


def test_pascal_case_aliases_are_keyed():
    assert loza.EmailHash("User@Example.com", secret=SECRET).value == _expected("user@example.com", SECRET)
    assert loza.IPHash("10.0.0.1", secret=SECRET).value == _expected("10.0.0.1", SECRET)


def test_empty_secret_is_rejected():
    with pytest.raises(ValueError, match="must not be empty"):
        loza.email_hash("user@example.com", secret="")
    with pytest.raises(ValueError, match="must not be empty"):
        loza.ip_hash("10.0.0.1", secret=b"")


def test_unconfigured_secret_is_ephemeral_not_unkeyed(monkeypatch):
    monkeypatch.delenv("LOZA_PII_HASH_SECRET", raising=False)
    monkeypatch.setattr(attr_module, "_ephemeral_hash_key", None)
    first = loza.email_hash("alice@example.com").value
    assert first == loza.email_hash("alice@example.com").value
    assert first != hashlib.sha256(b"alice@example.com").hexdigest()
    assert first != _expected("alice@example.com", b"guessed-secret")


def test_ephemeral_key_differs_across_processes():
    env = dict(os.environ)
    env.pop("LOZA_PII_HASH_SECRET", None)
    env["PYTHONPATH"] = str(Path(__file__).resolve().parents[1] / "src")
    code = "import loza; print(loza.email_hash('alice@example.com').value)"
    digests = [subprocess.check_output([sys.executable, "-c", code], env=env, text=True).strip() for _ in range(2)]
    assert digests[0] != digests[1]
