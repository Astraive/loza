#!/usr/bin/env python3
"""Regression tests for marker uniqueness and stale-row rejection (issue #948)."""

import importlib.util
from pathlib import Path

_MODULE_PATH = Path(__file__).with_name("test_equivalence.py")
_spec = importlib.util.spec_from_file_location("equivalence_under_test", _MODULE_PATH)
equivalence = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(equivalence)


def test_make_marker_is_collision_free_within_same_second(monkeypatch):
    """Two emits in the same second must not reuse an event_id."""
    # Pin the clock to prove the old second-resolution scheme collides.
    monkeypatch.setattr(equivalence.time, "time", lambda: 1_700_000_000.9)
    legacy = [f"equiv-{int(equivalence.time.time())}" for _ in range(2)]
    assert legacy[0] == legacy[1]  # the reported defect

    markers = [equivalence.make_marker("equiv") for _ in range(2)]
    assert markers[0] != markers[1]
    assert len(set(markers)) == 2
    assert all(marker.startswith("equiv-") for marker in markers)


def test_require_unique_event_rejects_stale_and_multiple_rows():
    """Verification must use exactly the just-emitted row, never events[0]."""
    fresh = {"event_id": "equiv-new", "event_name": "test.cross-sdk"}
    stale = {"event_id": "equiv-old", "event_name": "test.cross-sdk"}

    assert equivalence.require_unique_event([fresh], "equiv-new") is fresh
    # A prior run's row sharing the marker makes the result ambiguous: reject it.
    assert equivalence.require_unique_event([stale, fresh], "equiv-new") is None
    assert equivalence.require_unique_event([], "equiv-new") is None
