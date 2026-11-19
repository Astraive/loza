from __future__ import annotations

from loza.core import redactor


def _payload() -> dict:
    return {
        "items": ({"password": "secret", "token": "abc", "note": "keep me"},),
        "pair": ({"password": "deep"}, "plain"),
        "mixed": [({"password": "nested"},)],
    }


def test_redact_keys_descends_into_tuples_and_preserves_tuple_type() -> None:
    payload = _payload()
    result = redactor.redact_keys("password", "token")(payload)

    assert isinstance(result["items"], tuple)
    assert result["items"][0]["password"] == redactor.REDACTED_VALUE
    assert result["items"][0]["token"] == redactor.REDACTED_VALUE
    assert result["items"][0]["note"] == "keep me"
    assert result["pair"][0]["password"] == redactor.REDACTED_VALUE
    assert result["pair"][1] == "plain"
    assert isinstance(result["mixed"], list)
    assert result["mixed"][0][0]["password"] == redactor.REDACTED_VALUE
    # Redaction operates on a deep copy and leaves the source untouched.
    assert payload["items"][0]["password"] == "secret"


def test_hash_keys_descends_into_tuples() -> None:
    result = redactor.hash_keys("token")(_payload())

    assert isinstance(result["items"], tuple)
    assert len(result["items"][0]["token"]) == 64
    assert result["items"][0]["token"] != "abc"


def test_drop_keys_descends_into_tuples() -> None:
    result = redactor.drop_keys("password")(_payload())

    assert isinstance(result["items"], tuple)
    assert "password" not in result["items"][0]
    assert "password" not in result["pair"][0]
    assert "password" not in result["mixed"][0][0]


def test_mask_keys_descends_into_tuples() -> None:
    result = redactor.mask_keys("password")(_payload())

    assert isinstance(result["items"], tuple)
    assert "*" in result["items"][0]["password"]
    assert result["items"][0]["password"] != "secret"


def test_redact_patterns_descends_into_tuples() -> None:
    payload = {"items": ({"note": "secret value", "keep": "fine"},)}
    result = redactor.redact_patterns(r"secret")(payload)

    assert isinstance(result["items"], tuple)
    assert result["items"][0]["note"] == redactor.REDACTED_VALUE
    assert result["items"][0]["keep"] == "fine"
