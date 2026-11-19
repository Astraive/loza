from __future__ import annotations

import json

import pytest

from loza.generated.spec_contract import (
    ALLOWED_KINDS,
    LOZA_EVENT_VERSION,
    LOZA_SPEC_VERSION,
    ValidationErrors,
    build_ingest_envelope,
    validate_flexible_json_bytes,
)


def _valid_event() -> dict[str, object]:
    return {
        "schema_version": LOZA_SPEC_VERSION,
        "event_version": LOZA_EVENT_VERSION,
        "event_id": "evt_envelope_regression",
        "timestamp": "2026-01-01T00:00:00Z",
        "service": "checkout",
        "event": "login.ok",
        "kind": sorted(ALLOWED_KINDS)[0],
    }


def _envelope_bytes() -> bytes:
    envelope = build_ingest_envelope([_valid_event()], "loza-py", "0.4.1", "checkout")
    # A complete ingest envelope carries `api_version` alongside `events`.
    assert "api_version" in envelope and "events" in envelope
    return json.dumps(envelope).encode("utf-8")


def test_full_ingest_envelope_with_api_version_is_validated_as_envelope() -> None:
    raw = _envelope_bytes()
    for strict in (False, True):
        validate_flexible_json_bytes(raw, strict)


def test_full_ingest_envelope_still_rejects_invalid_events() -> None:
    envelope = json.loads(_envelope_bytes())
    envelope["events"] = [{"event": "incomplete"}]
    with pytest.raises(ValidationErrors):
        validate_flexible_json_bytes(json.dumps(envelope).encode("utf-8"), False)


def test_wrapper_without_api_version_still_validates_events() -> None:
    envelope = json.loads(_envelope_bytes())
    envelope.pop("api_version")
    validate_flexible_json_bytes(json.dumps(envelope).encode("utf-8"), True)
