"""Regression tests for strict-mode handling of the legacy ``event_type`` alias."""

from __future__ import annotations

from copy import deepcopy

from loza_contract import validate_event_payload_detailed


def _alias_payload() -> dict[str, object]:
    return {
        "schema_version": "v1",
        "event_version": "v1",
        "event_id": "evt_strict_alias",
        "timestamp": "2026-05-19T14:12:22Z",
        "service": "checkout",
        "event_type": "checkout.request",
        "kind": "event",
    }


def test_strict_mode_rejects_event_type_alias_before_normalization() -> None:
    payload = _alias_payload()
    original = deepcopy(payload)

    errors = validate_event_payload_detailed(payload, strict=True)

    alias_errors = [error for error in errors if error.code == "alias_not_allowed"]
    assert len(alias_errors) == 1
    assert alias_errors[0].field == "event_type"
    assert 'use "event" instead' in alias_errors[0].message
    # Validation must stay non-mutating even when it rejects the alias.
    assert payload == original


def test_strict_mode_does_not_report_the_normalized_alias_as_unknown() -> None:
    errors = validate_event_payload_detailed(_alias_payload(), strict=True)

    unknown_fields = [error.field for error in errors if error.code == "unknown_strict_field"]
    assert "event_type" not in unknown_fields


def test_loose_mode_still_accepts_event_type_alias() -> None:
    errors = validate_event_payload_detailed(_alias_payload(), strict=False)

    assert all(error.code != "alias_not_allowed" for error in errors)
    assert all(error.code != "unknown_strict_field" for error in errors)
