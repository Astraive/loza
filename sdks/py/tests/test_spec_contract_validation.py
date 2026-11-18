"""Regression tests for validate_event_map required-field enforcement."""

from __future__ import annotations

import pytest

import loza
from loza.core.spec_contract import REQUIRED_EVENT_FIELDS, validate_event_map


def _snake_case_event() -> dict[str, object]:
    return {
        "schema_version": "v1",
        "event_version": "v1",
        "event_id": "evt_1",
        "timestamp": "2026-01-01T00:00:00Z",
        "service": "checkout",
        "event": "payment.completed",
    }


def test_all_required_fields_present_is_valid() -> None:
    valid, errors = validate_event_map(_snake_case_event())
    assert valid is True
    assert errors == []


@pytest.mark.parametrize("missing", sorted(REQUIRED_EVENT_FIELDS))
def test_each_required_field_is_enforced(missing: str) -> None:
    event = _snake_case_event()
    del event[missing]
    valid, errors = validate_event_map(event)
    assert valid is False
    assert f"missing required field: {missing}" in errors


@pytest.mark.parametrize("missing", ["schema_version", "event_version"])
def test_missing_version_fields_are_rejected(missing: str) -> None:
    """Regression: version fields are required but were previously unchecked."""
    event = _snake_case_event()
    del event[missing]
    valid, errors = validate_event_map(event)
    assert valid is False
    assert f"missing required field: {missing}" in errors


def test_canonical_camel_case_aliases_satisfy_required_fields() -> None:
    event = {
        "schemaVersion": "v1",
        "eventVersion": "v1",
        "eventId": "evt_1",
        "timestamp": "2026-01-01T00:00:00Z",
        "service": "checkout",
        "event": "payment.completed",
    }
    valid, errors = validate_event_map(event)
    assert valid is True
    assert errors == []


def test_public_validate_event_delegates_to_contract() -> None:
    event = _snake_case_event()
    del event["schema_version"]
    valid, errors = loza.validate_event(event)
    assert valid is False
    assert any("schema_version" in error for error in errors)
