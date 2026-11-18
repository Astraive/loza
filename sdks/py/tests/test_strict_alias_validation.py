from __future__ import annotations

import json
from copy import deepcopy
from pathlib import Path

import pytest

from loza.generated.spec_contract import (
    ValidationErrors,
    validate_event_payload,
    validate_event_payload_detailed,
)


def _fixture(relative_path: str) -> dict[str, object]:
    root = Path(__file__).resolve().parents[3]
    return json.loads((root / "spec" / "fixtures" / relative_path).read_text())


def _error_codes(payload: dict[str, object], strict: bool) -> list[str]:
    return [error.code for error in validate_event_payload_detailed(payload, strict)]


def test_strict_mode_rejects_event_type_alias_before_normalization() -> None:
    payload = _fixture("invalid/strict_event_type_alias.json")

    errors = validate_event_payload_detailed(payload, strict=True)

    alias_errors = [error for error in errors if error.code == "alias_not_allowed"]
    assert len(alias_errors) == 1
    assert alias_errors[0].field == "event_type"
    with pytest.raises(ValidationErrors):
        validate_event_payload(payload, strict=True)


def test_strict_mode_rejects_event_type_alias_without_canonical_event() -> None:
    payload = _fixture("invalid/strict_event_type_alias.json")
    payload.pop("event")

    assert "alias_not_allowed" in _error_codes(payload, strict=True)


def test_strict_mode_validation_does_not_mutate_payload() -> None:
    payload = _fixture("invalid/strict_event_type_alias.json")
    original = deepcopy(payload)

    validate_event_payload_detailed(payload, strict=True)

    assert payload == original
    assert "event_type" in payload


def test_loose_mode_still_accepts_event_type_alias() -> None:
    payload = _fixture("valid/loose_event_type_alias.json")

    # Loose mode normalizes the legacy alias away and accepts the payload.
    assert _error_codes(payload, strict=False) == []
    # The same payload is rejected in strict mode because the alias is present.
    assert "alias_not_allowed" in _error_codes(payload, strict=True)


def test_strict_mode_accepts_event_without_alias() -> None:
    payload = _fixture("invalid/strict_event_type_alias.json")
    payload.pop("event_type")

    assert _error_codes(payload, strict=True) == []
