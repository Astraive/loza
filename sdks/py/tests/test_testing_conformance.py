from __future__ import annotations

import json

import pytest

import loza
from loza.testkit.helpers import golden_test


def test_testing_and_conformance_helpers():
    logger, sink = loza.TestLogger()
    logger.info("hello")
    events = loza.DecodeEvents(sink)

    found = loza.expect_event(events, message="hello")
    assert found is not None
    loza.expect_attr(found, "message", "hello")

    snap = loza.snapshot_event(found)
    assert "event_id" not in snap
    assert "timestamp" not in snap
    assert isinstance(loza.mock_sink(), loza.MemorySink)

    loza.set_id_generator(lambda: "evt_fixed")
    logger2, sink2 = loza.TestLogger()
    logger2.info("id-fixed")
    fixed = loza.DecodeEvents(sink2)[0]
    assert fixed.get("event_id") == "evt_fixed"
    loza.reset_for_test()


def test_testkit_events_helpers_capture_emitted_events():
    from loza.testkit import helpers as testkit

    testkit.reset_for_test()
    kit = testkit.testkit("testkit-events")
    logger = kit["logger"]

    ctx = logger.start_event(loza.Params(event="captured"))
    logger.finish(ctx, "success")
    logger.emit(ctx)

    events = kit["events"]()
    assert len(events) == 1
    assert events[0]["event"] == "captured"
    assert kit["last_event"]()["event"] == "captured"

    # Module-level helpers share the same capture buffer as the testkit dict.
    assert testkit.events() == events
    assert testkit.last_event()["event"] == "captured"

    kit["clear_events"]()
    assert kit["events"]() == []
    assert testkit.last_event() is None
    testkit.reset_for_test()


def test_golden_test_missing_file_does_not_silently_baseline(tmp_path):
    """A missing golden must fail instead of writing the snapshot and passing."""
    target = tmp_path / "nested" / "golden.json"
    event = {"event": "test", "message": "hello"}

    with pytest.raises(FileNotFoundError):
        golden_test(str(target), event)

    # The critical regression: no golden file was created as a side effect.
    assert not target.exists()


def test_golden_test_update_flag_creates_and_compares(tmp_path):
    """An explicit update flag rebaselines; subsequent calls compare normally."""
    target = tmp_path / "golden.json"
    event = {"event": "test", "message": "hello", "event_id": "ignored"}

    assert golden_test(str(target), event, update=True) is True
    assert target.exists()
    assert json.loads(target.read_text(encoding="utf-8")) == loza.snapshot_event(event)

    # Existing golden is compared: a matching event passes, a differing one fails.
    assert golden_test(str(target), event) is True
    assert golden_test(str(target), {"event": "test", "message": "changed"}) is False

    # Re-running with update=True rebaselines an existing golden.
    golden_test(str(target), {"event": "test", "message": "changed"}, update=True)
    assert golden_test(str(target), {"event": "test", "message": "changed"}) is True
def test_snapshot_event_is_independent_of_later_event_mutation():
    event = {
        "event_id": "evt_1",
        "timestamp": "2026-01-01T00:00:00Z",
        "event": "test",
        "context": {"user": {"id": 1}, "tags": ["a", "b"]},
    }

    snap = loza.snapshot_event(event)

    assert "event_id" not in snap
    assert "timestamp" not in snap

    # Mutating nested values in the original event must not alter the snapshot.
    event["context"]["user"]["id"] = 2
    event["context"]["tags"].append("c")
    event["context"]["new"] = "value"

    assert snap["context"] == {"user": {"id": 1}, "tags": ["a", "b"]}
    assert snap["context"]["user"] is not event["context"]["user"]
    assert snap["context"]["tags"] is not event["context"]["tags"]
