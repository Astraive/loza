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
"""Regression tests for required-field validation in the spec contract."""

from loza.core.spec_contract import validate_event_map


def test_missing_event_id_reports_single_alias_agnostic_error():
    """A missing ID is one logical requirement, not two independent fields."""
    valid, errors = validate_event_map(
        {"timestamp": "2026-01-01T00:00:00Z", "service": "checkout", "event": "payment.completed"}
    )

    assert valid is False
    id_errors = [error for error in errors if "event_id" in error or "eventId" in error]
    assert len(id_errors) == 1, f"expected one ID error, got {id_errors}"
    assert id_errors[0] == "missing required field: event_id (or eventId)"


def test_event_id_snake_case_alias_satisfies_requirement():
    valid, errors = validate_event_map(
        {
            "schema_version": "v1",
            "event_version": "v1",
            "event_id": "evt_1",
            "timestamp": "2026-01-01T00:00:00Z",
            "service": "checkout",
            "event": "payment.completed",
        }
    )

    assert valid is True
    assert errors == []


@pytest.mark.parametrize("missing", sorted(REQUIRED_EVENT_FIELDS))
def test_each_required_field_is_enforced(missing: str) -> None:
    event = _snake_case_event()
    del event[missing]
    valid, errors = validate_event_map(event)
    assert valid is False
    expected = (
        "missing required field: event_id (or eventId)"
        if missing == "event_id"
        else f"missing required field: {missing}"
    )
    assert expected in errors


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
def test_event_id_camel_case_alias_satisfies_requirement():
    valid, errors = validate_event_map(
        {
            "schemaVersion": "v1",
            "eventVersion": "v1",
            "eventId": "evt_1",
            "timestamp": "2026-01-01T00:00:00Z",
            "service": "checkout",
            "event": "payment.completed",
        }
    )

    assert valid is True
    assert errors == []


def test_public_validate_event_delegates_to_contract() -> None:
    event = _snake_case_event()
    del event["schema_version"]
    valid, errors = loza.validate_event(event)
    assert valid is False
    assert any("schema_version" in error for error in errors)
def test_missing_all_fields_still_reports_one_id_error():
    valid, errors = validate_event_map({})

    assert valid is False
    id_errors = [error for error in errors if "event_id" in error or "eventId" in error]
    assert len(id_errors) == 1, f"expected one ID error, got {id_errors}"
