#!/usr/bin/env python3
"""Regression tests for polling-based synchronization in test_equivalence.py.

Guards against reintroducing fixed ``time.sleep(0.5)`` calls as a proxy for
"storage is ready": that both slows fast runs and flakes on slow storage.
"""

import importlib.util
import unittest
from pathlib import Path

_MODULE_PATH = Path(__file__).with_name("test_equivalence.py")


def _load_module():
    spec = importlib.util.spec_from_file_location("equivalence_under_test", _MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class FakeClock:
    """Deterministic clock advanced explicitly by the fake sleeper."""

    def __init__(self):
        self.now = 0.0

    def __call__(self):
        return self.now

    def advance(self, seconds):
        self.now += seconds


class PollingWaitTests(unittest.TestCase):
    def setUp(self):
        self.equivalence = _load_module()

    def test_waits_until_marker_appears_without_fixed_sleep(self):
        """Slow storage: early polls are empty, then the event appears."""
        clock = FakeClock()
        sleeps = []
        rows = [{"event_id": "equiv-1"}]
        polls = []

        def query_fn(marker):
            polls.append(marker)
            if len(polls) < 3:
                return []
            return rows

        result = self.equivalence.wait_for_event(
            "equiv-1",
            query_fn=query_fn,
            interval=0.05,
            deadline=5.0,
            sleep_fn=lambda seconds: sleeps.append(seconds),
            clock_fn=clock,
        )

        self.assertEqual(result, rows)
        self.assertEqual(len(polls), 3)
        self.assertEqual(len(sleeps), 2)
        # Retries use short bounded intervals, never a fixed 0.5s sleep.
        self.assertTrue(all(0 < s <= 0.05 for s in sleeps))

    def test_returns_promptly_when_event_already_present(self):
        """Fast storage: a single successful poll means no sleeping at all."""
        clock = FakeClock()
        sleeps = []
        rows = [{"event_id": "equiv-fast"}]
        polls = []

        def query_fn(marker):
            polls.append(marker)
            return rows

        result = self.equivalence.wait_for_event(
            "equiv-fast",
            query_fn=query_fn,
            interval=0.05,
            deadline=5.0,
            sleep_fn=lambda seconds: sleeps.append(seconds),
            clock_fn=clock,
        )

        self.assertEqual(result, rows)
        self.assertEqual(len(polls), 1)
        self.assertEqual(sleeps, [])

    def test_stops_at_bounded_deadline_when_marker_never_appears(self):
        """A missing marker stops at the deadline; it never loops forever."""
        clock = FakeClock()
        sleeps = []

        def query_fn(marker):
            return []

        def sleep_fn(seconds):
            sleeps.append(seconds)
            clock.advance(seconds)

        result = self.equivalence.wait_for_event(
            "equiv-missing",
            query_fn=query_fn,
            interval=1.0,
            deadline=3.0,
            sleep_fn=sleep_fn,
            clock_fn=clock,
        )

        self.assertEqual(result, [])
        self.assertLessEqual(clock(), 3.0)
        self.assertEqual(len(sleeps), 3)

    def test_emit_sites_use_polling_instead_of_fixed_sleep(self):
        """Source guard: no fixed sleeps remain as synchronization primitives."""
        source = _MODULE_PATH.read_text(encoding="utf-8")
        self.assertNotIn("time.sleep(0.5)", source)
        # One helper definition plus the three emit/query call sites.
        self.assertGreaterEqual(source.count("wait_for_event("), 4)


if __name__ == "__main__":
    unittest.main()
