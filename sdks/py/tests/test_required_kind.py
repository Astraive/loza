from __future__ import annotations

import pytest

from loza_contract import (
    ValidationErrors,
    validate_event_payload,
    validate_event_payload_detailed,
)


def _valid_event() -> dict[str, object]:
    return {
        "schema_version": "v1",
        "event_version": "v1",
        "event_id": "evt_1",
        "timestamp": "2026-05-12T00:00:00Z",
        "service": "checkout",
        "event": "payment.completed",
        "kind": "event",
    }


@pytest.mark.parametrize("strict", [True, False])
def test_absent_kind_is_rejected(strict: bool) -> None:
    payload = _valid_event()
    del payload["kind"]

    errors = validate_event_payload_detailed(payload, strict)

    assert any(error.field == "kind" and error.code == "missing_kind" for error in errors)


@pytest.mark.parametrize("kind_value", [None, "", "   ", 123])
@pytest.mark.parametrize("strict", [True, False])
def test_missing_or_non_string_kind_is_rejected(kind_value: object, strict: bool) -> None:
    payload = _valid_event()
    payload["kind"] = kind_value

    errors = validate_event_payload_detailed(payload, strict)

    assert any(error.field == "kind" and error.code == "missing_kind" for error in errors)


@pytest.mark.parametrize("strict", [True, False])
def test_valid_kind_is_still_accepted(strict: bool) -> None:
    assert validate_event_payload_detailed(_valid_event(), strict) == []


def test_invalid_enum_kind_is_still_rejected() -> None:
    payload = _valid_event()
    payload["kind"] = "not-a-kind"

    errors = validate_event_payload_detailed(payload, strict=True)

    assert any(error.field == "kind" and error.code == "invalid_enum" for error in errors)


def test_validate_event_payload_raises_for_absent_kind() -> None:
    payload = _valid_event()
    del payload["kind"]

    with pytest.raises(ValidationErrors) as exc_info:
        validate_event_payload(payload, strict=True)

    assert any(error.field == "kind" and error.code == "missing_kind" for error in exc_info.value.errors)
