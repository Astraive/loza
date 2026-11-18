"""Regression tests for the unconditional max event size limit.

The legacy ``validate_flexible_json_bytes`` guard only enforced
``max_event_size_bytes`` when the raw payload contained neither the substring
``b'\"events\"'`` nor a newline. An otherwise-valid single event could therefore
bypass the limit simply by carrying a field whose value is the string
``"events"`` (or by being pretty-printed). These tests pin the corrected
behaviour: a hard raw-byte ceiling plus per-event size validation decided by
parsing, not by substring sniffing.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

import loza_contract as lc  # noqa: E402


def _valid_event(**extra: Any) -> dict[str, Any]:
    event: dict[str, Any] = {
        "schema_version": lc.LOZA_SPEC_VERSION,
        "event_version": lc.LOZA_EVENT_VERSION,
        "event_id": "evt-1",
        "event": "http.request",
        "timestamp": "2026-01-01T00:00:00Z",
        "service": "api",
        "kind": "http",
    }
    event.update(extra)
    return event


def _assert_too_large(raw: bytes) -> None:
    with pytest.raises(lc.ValidationErrors) as exc_info:
        lc.validate_flexible_json_bytes(raw, False)
    assert exc_info.value.errors[0].code == "too_large"


def test_oversized_single_event_with_events_substring_is_rejected() -> None:
    """An oversized single event whose value is literally "events" must not skip the limit."""
    payload = _valid_event(marker="events", blob="x" * (lc.MAX_EVENT_BYTES + 1000))
    raw = json.dumps(payload).encode()
    assert b'"events"' in raw
    assert len(raw) > lc.MAX_EVENT_BYTES
    _assert_too_large(raw)


def test_oversized_pretty_printed_single_event_is_rejected() -> None:
    """A newline in the raw JSON must not bypass the single-event limit."""
    payload = _valid_event(blob="y" * (lc.MAX_EVENT_BYTES + 1000))
    raw = json.dumps(payload, indent=1).encode()
    assert b"\n" in raw
    assert len(raw) > lc.MAX_EVENT_BYTES
    _assert_too_large(raw)


def test_oversized_event_in_wrapper_is_rejected() -> None:
    """Per-event size is validated inside an ingest wrapper."""
    wrapper = {"events": [_valid_event(blob="a" * (lc.MAX_EVENT_BYTES + 1000))]}
    raw = json.dumps(wrapper).encode()
    assert len(raw) < lc.MAX_BATCH_BYTES  # fail is due to the single event, not the raw cap
    _assert_too_large(raw)


def test_oversized_ndjson_line_is_rejected() -> None:
    """Each NDJSON line is an individual event and is size-checked."""
    line = json.dumps(_valid_event(blob="b" * (lc.MAX_EVENT_BYTES + 1000)))
    raw = (line + "\n" + json.dumps(_valid_event(event_id="evt-2")) + "\n").encode()
    _assert_too_large(raw)


def test_raw_payload_beyond_hard_cap_is_rejected_unconditionally() -> None:
    """The whole payload has an unconditional raw-byte ceiling regardless of shape."""
    payload = {"events": [_valid_event(blob="c" * (lc.MAX_BATCH_BYTES // 2)) for _ in range(2)]}
    raw = json.dumps(payload).encode()
    assert len(raw) > lc.MAX_BATCH_BYTES
    assert b'"events"' in raw  # would have bypassed the legacy guard
    with pytest.raises(lc.ValidationErrors) as exc_info:
        lc.validate_flexible_json_bytes(raw, False)
    assert exc_info.value.errors[0].code == "too_large"
    assert "max_batch_size_bytes" in exc_info.value.errors[0].message


def test_valid_single_event_and_batches_still_accepted() -> None:
    """The fix must not reject legitimate payloads under the limits."""
    lc.validate_flexible_json_bytes(json.dumps(_valid_event()).encode(), False)
    lc.validate_flexible_json_bytes(
        json.dumps({"events": [_valid_event(event_id="e1"), _valid_event(event_id="e2")]}).encode(),
        False,
    )
    lc.validate_flexible_json_bytes(
        (json.dumps(_valid_event(event_id="e1")) + "\n" + json.dumps(_valid_event(event_id="e2")) + "\n").encode(),
        False,
    )
