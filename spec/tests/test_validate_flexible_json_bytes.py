"""Regression tests for the max_event_size_bytes bypass.

A payload that merely contained the substring ``"events"`` or an embedded
newline used to skip the raw byte-size check entirely. The hard bound is now
shape-independent, and per-event limits are applied after parsing.
"""

from __future__ import annotations

import importlib.util
import json
import sys
from pathlib import Path
from typing import Any

import pytest


def _load_contract() -> Any:
    path = Path(__file__).resolve().parents[1] / "generated" / "python" / "loza_contract.py"
    spec = importlib.util.spec_from_file_location("loza_contract_generated", path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


contract = _load_contract()

VALID_EVENT: dict[str, Any] = {
    "schema_version": "v1",
    "event_version": "v1",
    "timestamp": "2026-05-12T00:00:00Z",
    "event_id": "evt_1",
    "service": "checkout",
    "event": "checkout.request",
    "kind": "http",
}


def _oversized_event() -> dict[str, Any]:
    event = dict(VALID_EVENT)
    event["data"] = "x" * (contract.MAX_EVENT_BYTES + 1024)
    return event


def _assert_rejected(raw: bytes) -> None:
    with pytest.raises(contract.ValidationErrors) as exc:
        contract.validate_flexible_json_bytes(raw, False)
    assert any(getattr(err, "code", None) == "too_large" for err in exc.value.errors)


def test_oversized_payload_with_events_key_is_rejected() -> None:
    # A single oversized object that contains an "events"-like key used to
    # bypass the global size check entirely.
    raw = json.dumps({"events": [_oversized_event()]}).encode()
    _assert_rejected(raw)


def test_oversized_wrapped_events_envelope_is_rejected() -> None:
    raw = json.dumps({"api_version": "v1", "events": [_oversized_event()]}).encode()
    _assert_rejected(raw)


def test_oversized_payload_with_embedded_newline_is_rejected() -> None:
    raw = ("\n" + json.dumps(_oversized_event()) + "\n").encode()
    _assert_rejected(raw)


def test_oversized_raw_payload_is_rejected() -> None:
    raw = ("x" * (contract.MAX_BATCH_BYTES + 1024)).encode()
    _assert_rejected(raw)


def test_oversized_ndjson_line_is_rejected() -> None:
    raw = (json.dumps(_oversized_event()) + "\n" + json.dumps(VALID_EVENT)).encode()
    _assert_rejected(raw)


def test_valid_payloads_still_pass() -> None:
    contract.validate_flexible_json_bytes(json.dumps(VALID_EVENT).encode(), False)
    contract.validate_flexible_json_bytes(
        json.dumps({"events": [VALID_EVENT]}).encode(), False
    )
    contract.validate_flexible_json_bytes(
        (json.dumps(VALID_EVENT) + "\n" + json.dumps(VALID_EVENT)).encode(), False
    )
