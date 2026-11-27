#!/usr/bin/env python3
"""Regression tests for the equivalence harness's pass/fail reporting.

MASS-0857: extended (v0.2.0) check failures used to be downgraded to WARN
prints while the script still exited 0 and printed ``PASSED``. These tests
drive ``main()`` against an in-memory fake collector so that a failed check
must produce a non-zero exit and must not be reported as a pass.
"""

import contextlib
import importlib.util
import io
import unittest
from pathlib import Path
from unittest import mock

_MODULE_PATH = Path(__file__).with_name("test_equivalence.py")
_spec = importlib.util.spec_from_file_location("equivalence_under_test", _MODULE_PATH)
equiv = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(equiv)


def _matches(marker, prefixes):
    return any(marker.startswith(prefix) for prefix in prefixes)


class FakeCollector:
    """In-memory stand-in for the collector endpoints used by main()."""

    def __init__(self, fail_emit_prefixes=(), drop_query_prefixes=(), corrupt_fields=()):
        self.store = {}
        self.fail_emit_prefixes = tuple(fail_emit_prefixes)
        self.drop_query_prefixes = tuple(drop_query_prefixes)
        self.corrupt_fields = tuple(corrupt_fields)

    def available(self):
        return True

    def emit(self, event):
        marker = event.get("event_id")
        if _matches(marker, self.fail_emit_prefixes):
            return None
        projected = self._project(event)
        for prefix, field in self.corrupt_fields:
            if marker.startswith(prefix):
                projected[field] = None
        self.store[marker] = projected
        return {"ok": True}

    def query(self, marker):
        if _matches(marker, self.drop_query_prefixes) or marker not in self.store:
            return []
        return [self.store[marker]]

    @staticmethod
    def _project(event):
        """Build the projected row shape used by the equivalence assertions."""
        return {
            "event_id": event.get("event_id"),
            "event_name": event.get("event"),
            "service": event.get("service"),
            "outcome": event.get("outcome"),
            "http_method": event.get("method"),
            "http_path": event.get("path"),
            "status_code": event.get("status_code"),
            "release": event.get("release"),
            "attrs": event.get("attrs"),
            "level": event.get("level"),
            "kind": event.get("kind"),
        }


def run_main(collector):
    """Run main() with network/sleep stubbed; return (exit_code, stdout)."""
    with mock.patch.object(equiv, "check_collector_available", collector.available), \
            mock.patch.object(equiv, "emit_event_via_api", collector.emit), \
            mock.patch.object(equiv, "query_events", collector.query), \
            mock.patch.object(equiv.time, "sleep", lambda *_: None), \
            contextlib.redirect_stdout(io.StringIO()) as output:
        exit_code = None
        try:
            equiv.main()
        except SystemExit as exc:
            exit_code = exc.code
    return exit_code, output.getvalue()


class EquivalenceResultTests(unittest.TestCase):
    def assert_failed(self, collector):
        exit_code, output = run_main(collector)
        self.assertEqual(exit_code, 1, "a failed extended check must exit 1")
        self.assertIn("Equivalence Test FAILED", output)
        self.assertNotIn("Equivalence Test PASSED", output)
        return output

    def test_all_extended_checks_pass(self):
        exit_code, output = run_main(FakeCollector())
        self.assertIn(exit_code, (None, 0))
        self.assertIn("Equivalence Test PASSED", output)
        self.assertNotIn("Equivalence Test FAILED", output)

    def test_release_emit_failure_fails_test(self):
        output = self.assert_failed(FakeCollector(fail_emit_prefixes=("equiv-release-",)))
        self.assertIn("Release field emit failed", output)

    def test_release_projection_failure_fails_test(self):
        output = self.assert_failed(FakeCollector(drop_query_prefixes=("equiv-release-",)))
        self.assertIn("Release or attrs did not match in projected storage", output)

    def test_notice_emit_failure_fails_test(self):
        output = self.assert_failed(FakeCollector(fail_emit_prefixes=("equiv-notice-",)))
        self.assertIn("Notice level emit failed", output)

    def test_kind_emit_failure_fails_test(self):
        output = self.assert_failed(FakeCollector(fail_emit_prefixes=("equiv-kind-",)))
        self.assertIn("kind=agent emit failed", output)
        self.assertIn("kind=ai emit failed", output)

    def test_corrupted_extended_fields_fail_equivalence(self):
        corruptions = (
            (("equiv-release-", "release"), "Release or attrs"),
            (("equiv-release-", "attrs"), "Release or attrs"),
            (("equiv-domain-", "attrs"), "Domain helper attrs"),
            (("equiv-notice-", "level"), "Notice level did not match"),
            (("equiv-kind-agent-", "kind"), "kind=agent did not match"),
            (("equiv-kind-ai-", "kind"), "kind=ai did not match"),
        )
        for corruption, failure in corruptions:
            with self.subTest(field=corruption[1], marker=corruption[0]):
                output = self.assert_failed(FakeCollector(corrupt_fields=(corruption,)))
                self.assertIn(failure, output)

    def test_domain_emit_failure_fails_test(self):
        output = self.assert_failed(FakeCollector(fail_emit_prefixes=("equiv-domain-",)))
        self.assertIn("Domain event emit failed", output)

    def test_domain_projection_failure_fails_test(self):
        output = self.assert_failed(FakeCollector(drop_query_prefixes=("equiv-domain-",)))
        self.assertIn("Domain helper attrs did not match in projected storage", output)
    def test_collector_unavailable_still_skips(self):
        with mock.patch.object(equiv, "check_collector_available", lambda: False), \
                contextlib.redirect_stdout(io.StringIO()) as output:
            exit_code = None
            try:
                equiv.main()
            except SystemExit as exc:
                exit_code = exc.code
        self.assertIn(exit_code, (None, 0))
        self.assertIn("SKIP", output.getvalue())


if __name__ == "__main__":
    unittest.main()
