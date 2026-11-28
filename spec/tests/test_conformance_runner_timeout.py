"""Regression tests for conformance runner subprocess timeouts."""

import io
import json
import subprocess
import sys
import time
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from unittest import mock

spec_dir = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(spec_dir / "conformance"))

import runner  # noqa: E402


def _check(*command):
    return runner.Check(
        sdk="python",
        group="timeout_probe",
        command=tuple(command),
        cwd=Path(__file__).resolve().parent,
        description="timeout regression probe",
    )


class TestRunCheckTimeout(unittest.TestCase):
    def test_hung_command_is_reported_as_a_failure_with_a_reason(self):
        check = _check(sys.executable, "-c", "import time; time.sleep(30)")
        started = time.perf_counter()
        result = runner._run_check(check, verbose=False, timeout=0.25)
        elapsed = time.perf_counter() - started

        self.assertLess(elapsed, 5.0, "the hung command was not stopped by the timeout")
        self.assertFalse(result.passed)
        self.assertEqual(result.returncode, runner.TIMEOUT_RETURN_CODE)
        self.assertIn("timed out", result.stderr)
        self.assertLessEqual(result.duration_s, 5.0)

    def test_fast_command_still_passes(self):
        result = runner._run_check(
            _check(sys.executable, "-c", "print('ok')"),
            verbose=False,
            timeout=5.0,
        )
        self.assertTrue(result.passed)
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout.strip(), "ok")

    def test_timeout_is_passed_to_subprocess(self):
        check = _check(sys.executable, "-c", "pass")
        with mock.patch.object(runner.subprocess, "run") as run:
            run.return_value = subprocess.CompletedProcess(args=(), returncode=0, stdout="", stderr="")
            runner._run_check(check, verbose=False, timeout=12.5)
        self.assertEqual(run.call_args.kwargs["timeout"], 12.5)

    def test_json_timeout_with_partial_bytes_emits_parseable_report(self):
        check = runner.Check(
            sdk="go",
            group="timeout_probe",
            command=(sys.executable, "-c", "print('unused')"),
            cwd=Path(__file__).resolve().parent,
            description="timeout JSON regression probe",
        )
        timeout = subprocess.TimeoutExpired(
            cmd=check.command,
            timeout=0.1,
            output=b"partial stdout \xc3\xa9\n",
            stderr=b"partial stderr\n",
        )
        stdout = io.StringIO()
        stderr = io.StringIO()
        with (
            mock.patch.object(runner, "SDK_GROUPS", {"go": [check]}),
            mock.patch.object(sys, "argv", ["runner.py", "--sdk", "go", "--verbose", "--json"]),
            mock.patch.object(runner.subprocess, "run", side_effect=timeout),
            mock.patch.object(runner, "_check_timeout_seconds", return_value=0.1),
            redirect_stdout(stdout),
            redirect_stderr(stderr),
        ):
            exit_code = runner.main()

        self.assertEqual(exit_code, 1)
        payload = json.loads(stdout.getvalue())
        self.assertEqual(
            payload["summary"],
            {"total": 1, "passed": 0, "failed": 1, "skipped": 0},
        )
        self.assertEqual(payload["checks"][0]["stdout"], "partial stdout é\n")
        self.assertIn("timed out after 0.1s", payload["checks"][0]["stderr"])
        self.assertIn("partial stderr", payload["checks"][0]["stderr"])
        self.assertIn("partial stdout é", stderr.getvalue())
        self.assertIn("partial stderr", stderr.getvalue())

    def test_sdk_probe_also_bounds_its_subprocess(self):
        with mock.patch.object(runner.subprocess, "run") as run:
            run.side_effect = subprocess.TimeoutExpired(cmd=["python"], timeout=1.0)
            self.assertFalse(runner._is_python_sdk_installed())
        self.assertEqual(run.call_args.kwargs["timeout"], runner.PROBE_TIMEOUT_SECONDS)


if __name__ == "__main__":
    unittest.main()
