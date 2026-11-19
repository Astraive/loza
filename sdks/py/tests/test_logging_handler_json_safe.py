from __future__ import annotations

import json
import logging
from datetime import datetime

import loza
from loza.core.config import Config
from loza.integrations.logging.handler import LozaHandler, _json_safe
from loza.sinks import MemorySink


def test_json_safe_rejects_nested_non_json_values() -> None:
    """A container is only safe when every member is serializable too."""
    assert _json_safe("ok")
    assert _json_safe(42)
    assert _json_safe(None)
    assert _json_safe({"a": [1, 2, {"b": "c"}]})
    assert _json_safe((1, "two"))

    # Shallow type checks previously admitted all of these, causing
    # json.dumps(payload, sort_keys=True) to raise TypeError downstream.
    assert not _json_safe({"ctx": {"ts": datetime.now()}})
    assert not _json_safe([datetime.now()])
    assert not _json_safe((datetime.now(),))
    assert not _json_safe({"outer": {"inner": [datetime.now()]}})

    cycle: list = []
    cycle.append(cycle)
    assert not _json_safe(cycle)


def test_attrs_drops_unsafe_extras_but_keeps_safe_ones() -> None:
    record = logging.LogRecord("svc", logging.INFO, "mod.py", 1, "hello", None, None)
    record.request_id = "abc-123"
    record.ctx = {"ts": datetime.now()}

    attrs = LozaHandler()._attrs(record)

    assert attrs["log.request_id"] == "abc-123"
    assert "log.ctx" not in attrs
    # A nested unsafe value must never reach the serializer's input.
    json.dumps(attrs, sort_keys=True)


def test_emit_with_nested_non_json_extra_does_not_lose_event() -> None:
    sink = MemorySink()
    loza.configure(Config.test("logging-handler").with_sink(sink))
    try:
        record = logging.LogRecord("svc", logging.INFO, "mod.py", 1, "hello", None, None)
        record.request_id = "abc-123"
        record.ctx = {"ts": datetime.now()}

        LozaHandler().emit(record)

        assert len(sink.events) == 1, "event was dropped instead of being emitted"
        payload = json.loads(sink.events[0])
        assert payload["message"] == "hello"
        assert payload["attrs"]["log"]["request_id"] == "abc-123"
        assert "ctx" not in payload["attrs"].get("log", {})
    finally:
        loza.reset()
