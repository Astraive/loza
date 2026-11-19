from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

import loza_contract as contract  # noqa: E402


VALID_EVENT = {
    "schema_version": "v1",
    "event_version": "v1",
    "event_id": "evt-1",
    "event": "test.event",
    "timestamp": "2026-01-01T00:00:00Z",
    "service": "test-service",
    "kind": "event",
}


def _ndjson(*lines: object) -> bytes:
    return "\n".join(json.dumps(line) for line in lines).encode()


def test_malformed_ndjson_line_raises_validation_errors() -> None:
    raw = (json.dumps(VALID_EVENT) + "\n{not valid json").encode()

    with pytest.raises(contract.ValidationErrors) as exc_info:
        contract.validate_flexible_json_bytes(raw, strict=False)

    assert exc_info.value.errors[0].code == "invalid_json"


def test_non_object_ndjson_line_raises_validation_errors() -> None:
    for line in (123, [1, 2, 3], "a string", True, None):
        raw = _ndjson(VALID_EVENT, line)

        with pytest.raises(contract.ValidationErrors) as exc_info:
            contract.validate_flexible_json_bytes(raw, strict=False)

        assert exc_info.value.errors[0].code == "invalid_payload"


def test_valid_ndjson_lines_still_validate() -> None:
    second_event = dict(VALID_EVENT, event_id="evt-2")
    raw = _ndjson(VALID_EVENT, second_event)

    contract.validate_flexible_json_bytes(raw, strict=False)


def test_invalid_ndjson_event_still_surfaces_validation_errors() -> None:
    broken = dict(VALID_EVENT)
    broken.pop("event_id")
    raw = _ndjson(VALID_EVENT, broken)

    with pytest.raises(contract.ValidationErrors) as exc_info:
        contract.validate_flexible_json_bytes(raw, strict=False)

    assert exc_info.value.errors[0].field == "event_id"
