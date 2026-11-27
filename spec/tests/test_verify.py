"""Regression tests for the conformance verifier's handling of skipped checks.

A missing fixture (or another unavailable prerequisite) must be reported as a
SKIP, not as a failed conformance check. See issue #929.
"""
import contextlib
import importlib.util
import io
import json
import os
import sys
import unittest
from pathlib import Path
from unittest.mock import patch

REPO_ROOT = Path(__file__).resolve().parents[2]
VERIFY_PATH = REPO_ROOT / "spec" / "conformance" / "verify.py"


def _load_verify():
    """Import verify.py as a module, undoing its import-time chdir side effect."""
    name = "_loza_verify_under_test"
    previous_cwd = os.getcwd()
    try:
        module_spec = importlib.util.spec_from_file_location(name, VERIFY_PATH)
        module = importlib.util.module_from_spec(module_spec)
        sys.modules[name] = module
        module_spec.loader.exec_module(module)
        return module
    finally:
        os.chdir(previous_cwd)


verify = _load_verify()


class TestSkippedSubcheck(unittest.TestCase):
    def test_skip_state_is_honored_by_check(self):
        def fn(sc):
            sc.skip("fixture not found")

        sc = verify._check("X", 1, "needs a fixture", fn)
        self.assertTrue(sc.skipped)
        self.assertFalse(sc.passed)
        self.assertEqual(sc.detail, "fixture not found")

    def test_skip_defaults_to_a_reason(self):
        sc = verify._check("X", 1, "needs a fixture", lambda s: s.skip())
        self.assertTrue(sc.skipped)
        self.assertEqual(sc.detail, "SKIPPED")

    def test_skipped_subcheck_does_not_fail_its_category(self):
        sc = verify._check("X", 1, "needs a fixture", lambda s: s.skip("fixture not found"))
        mc = verify.MainCheck("X", "Example", [sc])
        self.assertEqual(mc.skipped, 1)
        self.assertEqual(mc.passed, 0)
        self.assertEqual(mc.total, 1)
        self.assertTrue(mc.all_passed)

    def test_genuine_failure_still_fails(self):
        def fn(sc):
            raise AssertionError("bad value")

        sc = verify._check("X", 1, "real check", fn)
        self.assertFalse(sc.skipped)
        self.assertFalse(sc.passed)
        self.assertFalse(verify.MainCheck("X", "Example", [sc]).all_passed)

    def test_skip_does_not_mask_a_genuine_failure(self):
        skipped = verify._check("X", 1, "needs a fixture", lambda s: s.skip("fixture not found"))
        failed = verify._check("X", 2, "real check", lambda s: (_ for _ in ()).throw(AssertionError("bad")))
        mc = verify.MainCheck("X", "Example", [skipped, failed])
        self.assertFalse(mc.all_passed)


class TestSkippedReporting(unittest.TestCase):
    def test_text_report_marks_skipped_as_skip_not_fail(self):
        sc = verify._check("X", 1, "needs a fixture", lambda s: s.skip("fixture not found"))
        buffer = io.StringIO()
        with contextlib.redirect_stdout(buffer):
            verify.print_report([verify.MainCheck("X", "Example", [sc])])
        output = buffer.getvalue()
        self.assertIn("[PASS]", output)
        self.assertIn("(1 skipped)", output)
        self.assertRegex(output, r"X\.01\s+SKIP\s+needs a fixture")
        self.assertNotIn("FAIL", output)

    def test_json_report_exposes_skip_state(self):
        sc = verify._check("X", 1, "needs a fixture", lambda s: s.skip("fixture not found"))
        buffer = io.StringIO()
        with contextlib.redirect_stdout(buffer):
            verify.print_json_report([verify.MainCheck("X", "Example", [sc])])
        report = json.loads(buffer.getvalue())
        self.assertEqual(report["total_skipped"], 1)
        self.assertEqual(report["categories"][0]["skipped"], 1)
        self.assertTrue(report["categories"][0]["subchecks"][0]["skipped"])


class TestCategorySelection(unittest.TestCase):
    def test_registered_category_runs_once_and_reports_main_check_id(self):
        calls = 0

        def check_timing_once():
            nonlocal calls
            calls += 1
            return verify.check_timing()

        check_timing_once.__name__ = "check_timing"
        registry = dict(verify.CATEGORY_REGISTRY)
        registry["TIMING"] = check_timing_once
        output = io.StringIO()
        with (
            patch.object(verify, "CATEGORY_REGISTRY", registry),
            patch.object(verify, "ALL_CATEGORIES", [check_timing_once]),
            patch.object(sys, "argv", ["verify.py", "--category", "TIMING", "--json"]),
            contextlib.redirect_stdout(output),
        ):
            exit_code = verify.main()

        report = json.loads(output.getvalue())
        self.assertEqual(exit_code, 0)
        self.assertEqual(calls, 1)
        self.assertEqual(report["categories"][0]["id"], "TIMING")
        self.assertEqual(report["total_checks"], 10)

    def test_unknown_category_lists_ids_without_running_checks(self):
        calls = []

        def check_timing_once():
            calls.append("called")
            return verify.check_timing()

        check_timing_once.__name__ = "check_timing"
        output = io.StringIO()
        with (
            patch.object(verify, "CATEGORY_REGISTRY", {"TIMING": check_timing_once}),
            patch.object(verify, "ALL_CATEGORIES", [check_timing_once]),
            patch.object(sys, "argv", ["verify.py", "--category", "UNKNOWN"]),
            contextlib.redirect_stdout(output),
        ):
            exit_code = verify.main()

        self.assertEqual(exit_code, 1)
        self.assertEqual(calls, [])
        self.assertIn("Unknown category: UNKNOWN", output.getvalue())
        self.assertIn("Available: TIMING", output.getvalue())


class TestMissingFixtureCallSites(unittest.TestCase):
    def test_collector_fixture_subchecks_are_skipped_not_failed(self):
        # Simulate the fixtures being unavailable -- the exact condition from
        # issue #929 that used to be printed as a FAIL.
        with patch.object(verify, "_load_fixture_response", return_value=None):
            mc = verify.check_collector()

        fixture_subchecks = mc.subchecks[:4]
        self.assertEqual(len(fixture_subchecks), 4)
        for sc in fixture_subchecks:
            self.assertTrue(sc.skipped, f"{sc.id} should be skipped")
            self.assertFalse(sc.passed)
            self.assertEqual(sc.detail, "fixture not found")
        self.assertEqual(mc.skipped, 4)
        self.assertEqual([sc for sc in mc.subchecks if not (sc.passed or sc.skipped)], [])
        self.assertTrue(mc.all_passed)


if __name__ == "__main__":
    unittest.main()
