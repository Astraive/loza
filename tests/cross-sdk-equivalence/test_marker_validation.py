"""Regression tests for safe SQL construction in test_equivalence.py.

The equivalence helper used to interpolate the ``marker`` argument directly
into the SQL sent to the collector's /query endpoint. A marker containing a
quote could terminate the string literal and alter the query. These tests pin
the validation behaviour that prevents that.
"""

import importlib.util
import unittest
from pathlib import Path
from unittest import mock

_MODULE_PATH = Path(__file__).with_name("test_equivalence.py")
_spec = importlib.util.spec_from_file_location("equivalence_under_test", _MODULE_PATH)
equivalence = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(equivalence)


class MarkerValidationTests(unittest.TestCase):
    def test_valid_marker_builds_expected_query(self):
        marker = "equiv-1234"
        sql = equivalence.build_events_query(marker)
        self.assertIn(f"event_id = '{marker}'", sql)

    def test_generated_markers_are_accepted(self):
        markers = (
            "equiv-1699999999",
            "equiv-release-1699",
            "equiv-kind-ai-1",
            "equiv-domain-4",
        )
        for marker in markers:
            with self.subTest(marker=marker):
                self.assertEqual(equivalence.validate_marker(marker), marker)

    def test_quote_in_marker_is_rejected(self):
        with self.assertRaises(ValueError):
            equivalence.build_events_query("x' OR '1'='1")

    def test_other_unsafe_markers_are_rejected(self):
        markers = (
            'x"; DROP TABLE events; --',
            "x;--",
            "raw payload?",
            "",
        )
        for marker in markers:
            with self.subTest(marker=marker), self.assertRaises(ValueError):
                equivalence.build_events_query(marker)

    def test_query_events_rejects_unsafe_marker_before_network_call(self):
        with mock.patch.object(equivalence.urllib.request, "urlopen") as urlopen:
            with self.assertRaises(ValueError):
                equivalence.query_events("bad'marker")
            urlopen.assert_not_called()


if __name__ == "__main__":
    unittest.main()
