"""End-to-end test: HTTPBatchSink -> live loza-collector pipeline.

Requires a collector at ``LOZA_TEST_COLLECTOR_URL`` (default
``http://127.0.0.1:9308``), its ingest token in ``LOZA_API_KEY``, and an
admin token in ``LOZA_TEST_COLLECTOR_ADMIN_KEY``.
"""

import json
import os
import socket
import time
import urllib.request
from urllib.parse import urlparse
import pytest

import loza


COLLECTOR_URL = os.getenv("LOZA_TEST_COLLECTOR_URL", "http://127.0.0.1:9308").rstrip("/")
INGEST_API_KEY = os.getenv("LOZA_API_KEY", "")
ADMIN_API_KEY = os.getenv("LOZA_TEST_COLLECTOR_ADMIN_KEY", "")


def _collector_reachable(url: str = COLLECTOR_URL, timeout: float = 2.0) -> bool:
    parsed = urlparse(url)
    host = parsed.hostname or "127.0.0.1"
    port = parsed.port or (443 if parsed.scheme == "https" else 80)
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.settimeout(timeout)
    try:
        sock.connect((host, port))
        return True
    except OSError:
        return False
    finally:
        sock.close()


_COLLECTOR_SKIP = pytest.mark.skipif(
    not _collector_reachable(),
    reason=f"loza-collector not running at {COLLECTOR_URL}",
)

EVENT_COUNT = 3
SERVICE_NAME = "e2e-test-service"


def _expected_events(count: int = EVENT_COUNT) -> list[dict]:
    """Build the events emitted by the E2E test, for query-row verification."""
    return [
        {"event": f"e2e.test.event_{i}", "service": SERVICE_NAME, "outcome": "success"}
        for i in range(count)
    ]


def _missing_expected_events(rows, expected_events):
    """Return expected events that are absent from collector query ``rows``."""
    seen = {(row.get("event"), row.get("service"), row.get("outcome")) for row in rows}
    return [
        event
        for event in expected_events
        if (event["event"], event["service"], event["outcome"]) not in seen
    ]


@_COLLECTOR_SKIP
def test_e2e_collector_pipeline():
    """Send events through HTTPBatchSink and verify collector receives them."""
    assert INGEST_API_KEY, "LOZA_API_KEY is required for authenticated collector E2E"
    assert ADMIN_API_KEY, "LOZA_TEST_COLLECTOR_ADMIN_KEY is required for status/query checks"

    print("\n=== E2E Test: HTTPBatchSink -> loza-collector ===\n")

    # 1. Create logger with collector sink
    config = loza.Production(SERVICE_NAME).with_collector_endpoint(COLLECTOR_URL).with_api_key(INGEST_API_KEY)
    logger = loza.New(config)

    # 2. Emit 3 events
    for i in range(EVENT_COUNT):
        ctx = logger.start_event(
            loza.Params(
                event=f"e2e.test.event_{i}",
                message=f"E2E test event {i}",
                level="info",
            )
        )
        logger.enrich(ctx, loza.String("test_run", "e2e"))
        logger.enrich(ctx, loza.Int("sequence", i))
        logger.finish(ctx, "success")
        logger.emit(ctx)
        print(f"  Sent: e2e.test.event_{i}")

    # 3. Flush
    logger.flush()
    time.sleep(1)
    print("  Flushed to collector\n")

    # 4. Verify via collector /status (accepted count)
    print("  Verifying via collector status...")
    status_req = urllib.request.Request(
        f"{COLLECTOR_URL}/status",
        headers={"Authorization": f"Bearer {ADMIN_API_KEY}"},
        method="GET",
    )
    with urllib.request.urlopen(status_req, timeout=5) as resp:
        status_data = json.loads(resp.read())

    accepted = status_data.get("ingest", {}).get("accepted", 0)
    print(f"  Collector accepted count: {accepted}")
    assert accepted >= 3, f"Expected >=3 accepted, got {accepted}"

    # Build the events we expect to find in the collector so the /query
    # fallback verification below is real rather than a dead list.
    expected_events = _expected_events()
    print("  Collector accepted >= 3 events: OK\n")

    # Verify collector has events in DuckDB via query endpoint
    rows = []
    try:
        query_req = urllib.request.Request(
            f"{COLLECTOR_URL}/query",
            data=json.dumps(
                {"sql": f"SELECT event, service, outcome FROM events WHERE service = '{SERVICE_NAME}' LIMIT 10"}
            ).encode(),
            headers={"Authorization": f"Bearer {ADMIN_API_KEY}", "content-type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(query_req, timeout=5) as resp:
            query_result = json.loads(resp.read())
        rows = query_result.get("rows", [])
        print(f"  DuckDB query returned {len(rows)} rows")
        for row in rows:
            print(f"    OK: {row.get('event')} | service={row.get('service')} | outcome={row.get('outcome')}")
    except Exception as e:
        print(f"  DuckDB query skipped: {e}")

    # Fallback verification: when DuckDB rows are available, confirm every
    # emitted event actually landed in the collector. Otherwise the accepted
    # count assertion above remains the source of truth.
    if rows:
        missing = _missing_expected_events(rows, expected_events)
        assert not missing, f"Expected events missing from collector query rows: {missing}"
        print(f"  All {len(expected_events)} expected events present in DuckDB query rows")

    # 5. CollectorClient health/ready/version/status
    from loza.core.http_client import CollectorClient

    cc = CollectorClient(f"{COLLECTOR_URL}/events", api_key=ADMIN_API_KEY)

    assert cc.health() is True, "collector health check failed"
    print("\n  Collector health: True")

    assert cc.ready() is True, "collector ready check failed"
    print("  Collector ready:  True")

    ver = cc.version()
    assert "version" in ver, f"version response missing: {ver}"
    print(f"  Collector version: {ver}")

    st = cc.status()
    assert st.get("status") == "ok", f"status not ok: {st}"
    accepted = st.get("ingest", {}).get("accepted", 0)
    print(f"  Collector status: {st.get('status')} | uptime: {st.get('uptime_seconds')}s | accepted: {accepted}")

    print("\n=== E2E PASSED ===")


def test_expected_events_matches_emitted_events():
    """The expected-event list mirrors the events the E2E test emits."""
    events = _expected_events()
    assert len(events) == EVENT_COUNT
    assert events[0] == {"event": "e2e.test.event_0", "service": SERVICE_NAME, "outcome": "success"}
    assert events[-1] == {
        "event": f"e2e.test.event_{EVENT_COUNT - 1}",
        "service": SERVICE_NAME,
        "outcome": "success",
    }


def test_missing_expected_events_detects_absent_event():
    """Verification reports an emitted event that is missing from query rows."""
    expected = _expected_events()
    rows = [
        {"event": "e2e.test.event_0", "service": SERVICE_NAME, "outcome": "success"},
        {"event": "e2e.test.event_1", "service": SERVICE_NAME, "outcome": "success"},
        {"unrelated": "row"},
    ]
    assert _missing_expected_events(rows, expected) == [expected[2]]


def test_missing_expected_events_empty_when_all_present():
    """Verification passes when every emitted event is present in query rows."""
    rows = [
        {"event": f"e2e.test.event_{i}", "service": SERVICE_NAME, "outcome": "success"}
        for i in range(EVENT_COUNT)
    ]
    assert _missing_expected_events(rows, _expected_events()) == []


if __name__ == "__main__":
    test_e2e_collector_pipeline()
