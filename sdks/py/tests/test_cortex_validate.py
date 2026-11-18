"""Regression tests for cortex event identifier validation."""

from __future__ import annotations

from dataclasses import asdict

from loza.cortex.schema import CortexEventSchema
from loza.cortex.validate import is_valid_cortex_event, validate_cortex_event


def test_valid_schema_event_has_incident_id_not_event_id() -> None:
    """A dict produced from CortexEventSchema carries incident_id, not event_id."""
    event = asdict(CortexEventSchema(incident_id="INC-714", severity="error"))
    assert "incident_id" in event
    assert "event_id" not in event


def test_validate_accepts_schema_event_with_incident_id() -> None:
    """Regression: valid events identified by incident_id must not be rejected."""
    event = asdict(CortexEventSchema(incident_id="INC-714", severity="error"))
    assert validate_cortex_event(event) == []
    assert is_valid_cortex_event(event) is True


def test_validate_rejects_event_without_identifier() -> None:
    """An event with neither identifier is invalid and reports the correct field."""
    assert validate_cortex_event({}) == ["missing incident_id"]
    assert is_valid_cortex_event({}) is False


def test_validate_accepts_legacy_event_id_alias() -> None:
    """Backward compatibility: callers still passing event_id remain valid."""
    assert validate_cortex_event({"event_id": "INC-legacy"}) == []
    assert is_valid_cortex_event({"event_id": "INC-legacy"}) is True
