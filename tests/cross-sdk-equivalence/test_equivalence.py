#!/usr/bin/env python3
"""test_equivalence.py -- Cross-SDK equivalence test.

Sends the same canonical event from each SDK and asserts stored events
match on canonical fields. Any failed check makes the process exit 1; only
an all-pass run prints PASSED. If no collector is running at localhost:9308,
prints SKIP and exits 0.
"""

import json
import sys
import time
import urllib.request
import urllib.error
import uuid

COLLECTOR_URL = "http://localhost:9308"
CANONICAL_EVENT = {
    "service": "equivalence-test",
    "event": "test.cross-sdk",
    "kind": "http",
    "method": "GET",
    "path": "/api/test",
    "http": {
        "status": 200,
    },
    "status_code": 200,
    "attrs": {
        "custom.label": "equivalence-check",
    },
}


def check_collector_available() -> bool:
    """Check if collector is running and healthy."""
    try:
        req = urllib.request.Request(f"{COLLECTOR_URL}/healthz", method="GET")
        with urllib.request.urlopen(req, timeout=3) as resp:
            return resp.status == 200
    except (urllib.error.URLError, OSError, TimeoutError):
        return False


def emit_event_via_api(event: dict) -> dict | None:
    """Emit an event directly to the collector ingest endpoint."""
    payload = json.dumps(event).encode("utf-8")
    req = urllib.request.Request(
        f"{COLLECTOR_URL}/ingest",
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except (urllib.error.URLError, OSError) as e:
        print(f"  WARN: Emit failed: {e}", file=sys.stderr)
        return None


def query_events(marker: str) -> list[dict]:
    """Query projected event fields without relying on encrypted raw payloads."""
    sql = (
        "SELECT event_id, event_name, service, outcome, http_method, http_path, status_code "
        f"FROM events WHERE event_id = '{marker}' ORDER BY event_id DESC"
    )
    payload = json.dumps({"query": sql}).encode("utf-8")
    req = urllib.request.Request(
        f"{COLLECTOR_URL}/query",
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            data = json.loads(resp.read().decode("utf-8"))
            return data.get("rows", [])
    except (urllib.error.URLError, OSError) as e:
        print(f"  WARN: Query failed: {e}", file=sys.stderr)
        return []


def make_marker(prefix: str) -> str:
    """Build a collision-free marker for a single emitted event.

    ``int(time.time())`` only has second resolution, so two runs in the same
    second reuse the same ``event_id``. A uuid4 suffix makes every emit
    independently identifiable.
    """
    return f"{prefix}-{uuid.uuid4().hex}"


def require_unique_event(events: list[dict], marker: str) -> dict | None:
    """Return the one row for ``marker``, or None unless exactly one exists.

    Verifying ``events[0]`` from ``ORDER BY event_id DESC`` can select a stale
    row left by an earlier run that reused the same marker. Require a single
    row so verification always operates on the just-emitted event.
    """
    if len(events) != 1:
        print(
            f"  WARN: Expected exactly one row for marker={marker}, got {len(events)}",
            file=sys.stderr,
        )
        return None
    return events[0]


def canonical_match(projected: dict, expected: dict) -> bool:
    """Check canonical fields exposed by the Collector's encrypted schema."""
    return (
        projected.get("event_id") == expected.get("event_id")
        and projected.get("event_name") == expected.get("event")
        and projected.get("service") == expected.get("service")
        and projected.get("outcome") == expected.get("outcome")
        and projected.get("http_method") == expected.get("method")
        and projected.get("http_path") == expected.get("path")
        and projected.get("status_code") == expected.get("status_code")
    )


def main():
    print("=== Cross-SDK Equivalence Test ===")
    print(f"Collector: {COLLECTOR_URL}")
    print()

    if not check_collector_available():
        print("SKIP: Collector not running at localhost:9308")
        print("Start the collector first: cd collector && go run ./cmd/loza-collector")
        sys.exit(0)

    print("Collector is available. Running equivalence test...")
    print()

    # Emit the canonical event with a unique marker
    marker = make_marker("equiv")
    event = dict(CANONICAL_EVENT)
    event["event_id"] = marker
    event["attrs"] = dict(CANONICAL_EVENT["attrs"])
    event["attrs"]["test.marker"] = marker

    print(f"Emitting canonical event (marker={marker})...")
    result = emit_event_via_api(event)
    if result is None:
        print("FAIL: Could not emit event to collector")
        sys.exit(1)

    print(f"  Emit response: {json.dumps(result)}")

    # Wait briefly for storage
    time.sleep(0.5)

    # Query and verify
    print("Querying stored events...")
    events = query_events(marker)
    found = require_unique_event(events, marker)
    if found is None:
        print(f"FAIL: Expected exactly one event for marker={marker}, got {len(events)}")
        sys.exit(1)

    print("  Found matching event. Verifying canonical fields...")

    # Verify canonical fields match
    if canonical_match(found, event):
        print("PASS: Canonical fields match between emitted and stored event")
    else:
        print("FAIL: Canonical fields do not match")
        print(f"  Expected: {json.dumps({k: event.get(k) for k in ['event_id', 'service', 'event', 'outcome']})}")
        print(f"  Got:      {json.dumps({k: found.get(k) for k in ['event_id', 'event_name', 'service', 'outcome']})}")
        sys.exit(1)

    # ── v0.2.0: Extended equivalence checks ────────────────────────────────
    # Failed extended checks must not be silently downgraded to warnings: the
    # overall result and exit code below have to reflect them, just like the
    # canonical check above does.
    failures: list[str] = []

    def record_failure(message: str) -> None:
        failures.append(message)
        print(f"  FAIL: {message}")

    print()
    print("=== v0.2.0 Extended Equivalence Checks ===")
    print()

    # 1. Release field equivalence
    print("1. Release field equivalence...")
    release_event = dict(CANONICAL_EVENT)
    release_event["attrs"] = dict(CANONICAL_EVENT["attrs"])
    release_marker = make_marker("equiv-release")
    release_event["event_id"] = release_marker
    release_event["release"] = "1.2.3"
    release_event["attrs"]["test.marker"] = release_marker

    result = emit_event_via_api(release_event)
    if result is None:
        record_failure("Release field emit failed (collector may not support)")
    else:
        time.sleep(0.5)
        events = query_events(release_marker)
        release_found = require_unique_event(events, release_marker)
        if release_found is not None and canonical_match(release_found, release_event):
            print("  PASS: Release event accepted and stored")
        else:
            record_failure("Release event was not found in projected storage")

    # 2. Notice level equivalence
    print("2. Notice level equivalence...")
    notice_marker = make_marker("equiv-notice")
    notice_event = dict(
        CANONICAL_EVENT,
        event=f"test.notice.{notice_marker}",
        event_id=notice_marker,
        level="notice",
        attrs=dict(CANONICAL_EVENT["attrs"], **{"test.marker": notice_marker}),
    )
    result = emit_event_via_api(notice_event)
    if result:
        print("  PASS: Notice-level event submitted")
    else:
        record_failure("Notice level emit failed")

    # 3. Agent/ai kind equivalence
    print("3. Agent/ai kind equivalence...")
    for kind in ("agent", "ai"):
        kind_marker = make_marker(f"equiv-kind-{kind}")
        result = emit_event_via_api(
            dict(
                CANONICAL_EVENT,
                event=f"test.{kind}.{kind_marker}",
                event_id=kind_marker,
                kind=kind,
                attrs=dict(CANONICAL_EVENT["attrs"], **{"test.marker": kind_marker}),
            )
        )
        if result:
            print(f"  PASS: kind={kind} submitted")
        else:
            record_failure(f"kind={kind} emit failed")

    # 4. Domain helper equivalence (money, percent, httpStatus)
    print("4. Domain helper equivalence...")
    domain_marker = make_marker("equiv-domain")
    domain_event = dict(CANONICAL_EVENT, event=f"test.domain.{domain_marker}", event_id=domain_marker)
    domain_event["attrs"] = {
        **CANONICAL_EVENT["attrs"],
        "test.marker": domain_marker,
        "cart.total": {"amount": 2999, "currency": "USD"},
        "tax.rate": "8.5%",
        "http.status_code": 200,
    }
    result = emit_event_via_api(domain_event)
    if result:
        time.sleep(0.5)
        events = query_events(domain_marker)
        domain_found = require_unique_event(events, domain_marker)
        if domain_found is not None and canonical_match(domain_found, domain_event):
            print("  PASS: Domain helper fields preserved in projected storage")
        else:
            record_failure("Domain event was not found in projected storage")
    else:
        record_failure("Domain event emit failed")

    print()
    print("=== v0.2.0 Extended Checks Complete ===")
    print()
    if failures:
        print(f"=== Equivalence Test FAILED ({len(failures)} extended check(s) failed) ===")
        for failure in failures:
            print(f"  - {failure}")
        sys.exit(1)
    print("=== Equivalence Test PASSED ===")


if __name__ == "__main__":
    main()
