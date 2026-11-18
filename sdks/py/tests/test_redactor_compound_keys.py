"""Regression tests for compound sensitive-key matching in the redactor."""

from loza.core import redactor


COMPOUND_SENSITIVE_KEYS = (
    "new_password",
    "password_confirmation",
    "user_api_key",
    "secret_value",
    "access_token_claims",
)


def _payload() -> dict[str, str]:
    return {key: "cleartext-value" for key in COMPOUND_SENSITIVE_KEYS}


def test_default_redactor_redacts_compound_sensitive_keys() -> None:
    result = redactor.default_redactor()(_payload())
    assert all(result[key] == redactor.REDACTED_VALUE for key in COMPOUND_SENSITIVE_KEYS)


def test_redact_keys_matches_compound_keys_and_custom_terms() -> None:
    payload = {"user_api_key": "v", "password_confirmation": "v", "db.client_secret": "v"}
    result = redactor.redact_keys("api_key", "password", "client_secret")(payload)
    assert all(result[key] == redactor.REDACTED_VALUE for key in payload)


def test_other_redaction_modes_match_compound_keys() -> None:
    payload = _payload()
    assert len(redactor.hash_keys("api_key")(payload)["user_api_key"]) == 64
    assert "new_password" not in redactor.drop_keys("password")(payload)
    assert "password_confirmation" not in redactor.drop_keys("password")(payload)
    assert "*" in redactor.mask_keys("secret")(payload)["secret_value"]


def test_compound_matching_does_not_leak_into_unrelated_keys() -> None:
    payload = {
        "username": "keep",
        "oauth_provider": "keep",
        "account_notes": "keep",
        "public_label": "keep",
    }
    assert redactor.default_redactor()(payload) == payload
