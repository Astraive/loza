from __future__ import annotations

import hashlib
import hmac

import pytest

import loza

_SECRET = b"typed-helper-test-secret"


def test_typed_attribute_helpers_snake_case():
    assert loza.money("price", 1000, "USD").key == "price"
    assert loza.percent("cpu", 85.5).key == "cpu"
    assert loza.bytes_attr("file.size", 1024).key == "file.size"
    assert loza.http_status("response.status", 200).key == "response.status"
    assert loza.status_code("code", 404).value == 404
    assert loza.error_code("err.code", "E123").value == "E123"
    assert loza.bucket("user.tier", "premium").key == "user.tier"
    assert loza.tags("env", "prod", "staging").key == "env"
    assert loza.masked("card", "4111111111111111").value == "41************11"
    assert loza.url("https://example.com").key == "url"
    expected_email = hmac.new(_SECRET, b"user@example.com", hashlib.sha256).hexdigest()
    expected_ip = hmac.new(_SECRET, b"192.168.1.1", hashlib.sha256).hexdigest()
    assert loza.email_hash("User@Example.COM", secret=_SECRET).value == expected_email
    assert loza.ip_hash("192.168.1.1", secret=_SECRET).value == expected_ip


def test_typed_attribute_helpers_pascal_case():
    assert loza.Money("price", 500, "EUR").key == "price"
    assert loza.Percent("mem", 72.3).key == "mem"
    assert loza.Bytes("disk", 2048).key == "disk"
    assert loza.HTTPStatus("status", 503).key == "status"
    assert loza.StatusCode("code", 404).value == 404
    assert loza.Bucket("env", "prod").key == "env"
    assert loza.Tags("region", "us", "eu").key == "region"
    assert loza.Masked("ssn", "123456789").value == "12*****89"
    assert loza.URL("https://loza.dev").key == "url"
    assert loza.EmailHash("test@test.com").key == "email.hash"
    assert loza.IPHash("10.0.0.1").key == "ip.hash"


def test_masked_zero_suffix_does_not_leak_value():
    # Regression: text[-0:] is the whole string, which previously appended the
    # entire plaintext after the stars and defeated masking.
    assert loza.masked("k", "abcdef", 2, 0).value == "ab****"
    assert loza.Masked("k", "abcdef", 2, 0).value == "ab****"


def test_masked_zero_prefix_does_not_leak_value():
    assert loza.masked("k", "abcdef", 0, 2).value == "****ef"
    assert loza.masked("k", "abcdef", 0, 0).value == "******"
def test_group_nested_key_over_conflicting_scalar():
    """A scalar at an intermediate key must not crash when a later attr nests under it."""
    group = loza.Group(loza.Int("a", 1), loza.Int("a.b", 2))
    assert group.value == {"a": {"b": 2}}

    # Deeper nesting through the conflicting scalar is handled as well.
    deep = loza.Group(loza.Int("a", 1), loza.Int("a.b.c", 2))
    assert deep.value == {"a": {"b": {"c": 2}}}

    # The existing last-writer-wins behavior for leaf keys is preserved.
    assert loza.Group(loza.Int("a", 1), loza.Int("a", 3)).value == {"a": 3}
def test_enum_rejects_value_outside_allowed_set():
    with pytest.raises(ValueError):
        loza.enum_("color", "purple", "red", "green", "blue")


def test_enum_accepts_value_inside_allowed_set():
    attr = loza.enum_("color", "red", "red", "green", "blue")
    assert attr.key == "color"
    assert attr.value == "red"


def test_enum_without_allowed_values_remains_unvalidated():
    attr = loza.enum_("color", "purple")
    assert attr.key == "color"
    assert attr.value == "purple"
