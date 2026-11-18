"""Regression tests for skipped-check accounting in conformance/runner.py.

A skipped check never executes, so it must not be reported as a pass. Before
the fix, ``_skip_result`` hard-coded ``passed=True``, the JSON summary counted
skips as passes, and a fully skipped run exited 0 with zero tests executed.
"""

import io
import json
import sys
import unittest
from contextlib import redirect_stderr, redirect_stdout
from importlib.util import module_from_spec, spec_from_file_location
from pathlib import Path
from unittest import mock

SPEC_DIR = Path(__file__).resolve().parents[1]
RUNNER_PATH = SPEC_DIR / "conformance" / "runner.py"


def _load_runner():
    spec = spec_from_file_location("loza_conformance_runner", RUNNER_PATH)
    module = module_from_spec(spec)
    # Dataclasses resolve string annotations via sys.modules on Python 3.14+.
    sys.modules[spec.name] = module
    try:
        spec.loader.exec_module(module)
    except BaseException:
        sys.modules.pop(spec.name, None)
        raise
    return module


runner = _load_runner()


def _check(group):
    return runner.Check("go", group, ("go", "test", "."), Path("/tmp"), "test check")


def _passed_result(check):
    # Constructing without the new skipped/skip_reason fields stays valid.
    return runner.CheckResult(
        sdk=check.sdk,
        group=check.group,
        description=check.description,
        command=check.command,
        cwd=str(check.cwd),
        passed=True,
        returncode=0,
        duration_s=0.0,
        stdout="",
        stderr="",
    )


class TestSkippedCheckAccounting(unittest.TestCase):
    def test_skip_result_is_explicitly_skipped_and_not_a_pass(self):
        with redirect_stdout(io.StringIO()):
            result = runner._skip_result(_check("collector_integration"), "no collector")
        self.assertTrue(result.skipped)
        self.assertFalse(result.passed)
        self.assertEqual(result.skip_reason, "no collector")

    def test_json_summary_separates_skipped_from_passed(self):
        passed = _passed_result(_check("state_machine"))
        with redirect_stdout(io.StringIO()):
            skipped = runner._skip_result(_check("collector_integration"), "no collector")

        out = io.StringIO()
        with redirect_stdout(out):
            runner._print_json([passed, skipped])
        payload = json.loads(out.getvalue())

        self.assertEqual(
            payload["summary"],
            {"total": 2, "passed": 1, "failed": 0, "skipped": 1},
        )
        self.assertFalse(payload["checks"][1]["passed"])
        self.assertTrue(payload["checks"][1]["skipped"])
        self.assertEqual(payload["checks"][1]["skip_reason"], "no collector")

    def test_all_skipped_run_exits_nonzero(self):
        skip_check = _check("collector_integration")
        with mock.patch.object(runner, "SDK_GROUPS", {"go": [skip_check]}), mock.patch.object(
            runner, "_is_collector_reachable", return_value=False
        ), mock.patch.object(
            sys, "argv", ["runner.py", "--sdk", "go", "--group", "collector_integration"]
        ), redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
            exit_code = runner.main()
        self.assertEqual(exit_code, 1)

    def test_partial_skip_warns_but_succeeds_when_executed_checks_pass(self):
        pass_check = _check("state_machine")
        skip_check = _check("collector_integration")
        stderr = io.StringIO()
        with mock.patch.object(runner, "SDK_GROUPS", {"go": [pass_check, skip_check]}), mock.patch.object(
            runner, "_run_check", return_value=_passed_result(pass_check)
        ), mock.patch.object(
            runner, "_is_collector_reachable", return_value=False
        ), mock.patch.object(
            sys, "argv", ["runner.py", "--sdk", "go", "--group", "all"]
        ), redirect_stdout(io.StringIO()), redirect_stderr(stderr):
            exit_code = runner.main()
        self.assertEqual(exit_code, 0)
        self.assertIn("WARNING", stderr.getvalue())
        self.assertIn("no collector", stderr.getvalue())


if __name__ == "__main__":
    unittest.main()
