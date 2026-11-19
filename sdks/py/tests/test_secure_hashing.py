import hashlib
import hmac

from loza.core import event as event_module
from loza.core import redactor, security
from loza.core.hashing import HASH_SECRET_ENV, secure_hash
from loza.core.logger import Logger

EMAIL = "alice@example.com"


def _plain_sha256(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def _keyed_sha256(secret: str, value: str) -> str:
    return hmac.new(secret.encode("utf-8"), value.encode("utf-8"), hashlib.sha256).hexdigest()


def test_secure_hash_is_keyed_not_plain_sha256(monkeypatch) -> None:
    """Low-entropy PII must not be recoverable with a generic SHA-256 rainbow table."""
    monkeypatch.setenv(HASH_SECRET_ENV, "unit-test-secret")
    result = secure_hash(EMAIL)

    assert result == _keyed_sha256("unit-test-secret", EMAIL)
    assert result != _plain_sha256(EMAIL)


def test_secure_hash_is_stable_for_a_configured_secret(monkeypatch) -> None:
    monkeypatch.setenv(HASH_SECRET_ENV, "unit-test-secret")
    assert secure_hash(EMAIL) == secure_hash(EMAIL)

    monkeypatch.setenv(HASH_SECRET_ENV, "a-different-secret")
    assert secure_hash(EMAIL) == _keyed_sha256("a-different-secret", EMAIL)
    assert secure_hash(EMAIL) != _keyed_sha256("unit-test-secret", EMAIL)


def test_secure_hash_falls_back_to_a_keyed_process_secret(monkeypatch) -> None:
    """With no configured secret the hash is still keyed, never plain SHA-256."""
    monkeypatch.delenv(HASH_SECRET_ENV, raising=False)
    first, second = secure_hash(EMAIL), secure_hash(EMAIL)

    assert first == second
    assert first != _plain_sha256(EMAIL)


def test_hash_at_path_uses_keyed_hash(monkeypatch) -> None:
    """Regression for MASS-0655: logger and event hashing must not use unsalted SHA-256."""
    monkeypatch.setenv(HASH_SECRET_ENV, "unit-test-secret")

    for hash_at_path in (Logger._hash_at_path, event_module._hash_at_path):
        target = {"user": {"email": EMAIL}}
        hash_at_path(target, "user.email")

        assert target["user"]["email"] == _keyed_sha256("unit-test-secret", EMAIL)
        assert target["user"]["email"] != _plain_sha256(EMAIL)


def test_security_and_redactor_hash_helpers_use_keyed_hash(monkeypatch) -> None:
    """Related hashing helpers must not reintroduce the same unsalted pattern."""
    monkeypatch.setenv(HASH_SECRET_ENV, "unit-test-secret")

    assert security.hash_value(EMAIL) == _keyed_sha256("unit-test-secret", EMAIL)
    hashed = redactor.hash_keys("email")({"email": EMAIL})["email"]
    assert hashed == _keyed_sha256("unit-test-secret", EMAIL)
    assert hashed != _plain_sha256(EMAIL)
