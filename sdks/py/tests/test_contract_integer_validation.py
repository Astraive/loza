"""Regression tests for integer contract fields rejecting booleans (issue #787)."""

from __future__ import annotations

import pytest

import loza_contract as lc


def _base_event() -> dict:
    return {
        "schema_version": lc.LOZA_SPEC_VERSION,
        "event_version": lc.LOZA_EVENT_VERSION,
        "event_id": "evt-1",
        "event": "test.event",
        "timestamp": "2024-01-01T00:00:00Z",
        "service": "svc",
        "kind": sorted(lc.ALLOWED_KINDS)[0],
    }


_EXPECTED_CODE = {"duration_ms": "invalid_integer", "status_code": "invalid_status_code"}


def _errors_for(payload: dict, key: str) -> list:
    return [error for error in lc.validate_event_payload_detailed(payload, False) if error.field == key]


@pytest.mark.parametrize("key", ["duration_ms", "status_code"])
@pytest.mark.parametrize("value", [True, False])
def test_boolean_rejected_for_integer_fields(key: str, value: bool) -> None:
    payload = _base_event()
    payload[key] = value
    assert any(error.code == _EXPECTED_CODE[key] for error in _errors_for(payload, key))


@pytest.mark.parametrize("key", ["duration_ms", "status_code"])
def test_real_integers_still_accepted(key: str) -> None:
    payload = _base_event()
    payload[key] = 250 if key == "status_code" else 42
    assert _errors_for(payload, key) == []


def test_negative_duration_still_rejected() -> None:
    payload = _base_event()
    payload["duration_ms"] = -1
    assert any(error.code == "invalid_integer" for error in _errors_for(payload, "duration_ms"))
