from __future__ import annotations

import loza


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
