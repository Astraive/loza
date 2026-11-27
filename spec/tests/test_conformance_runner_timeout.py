"""Regression tests for conformance runner subprocess timeouts."""

import subprocess
import sys
import time
import unittest
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

    def test_timeout_preserves_output_from_each_stream(self):
        cases = (
            (
                "stdout",
                "import time; print('partial stdout', flush=True); time.sleep(30)",
                "partial stdout\n",
                None,
            ),
            (
                "stderr",
                "import sys, time; "
                "print('partial stderr', file=sys.stderr, flush=True); "
                "time.sleep(30)",
                "",
                "partial stderr",
            ),
            (
                "both",
                "import sys, time; "
                "print('partial stdout', flush=True); "
                "print('partial stderr', file=sys.stderr, flush=True); "
                "time.sleep(30)",
                "partial stdout\n",
                "partial stderr",
            ),
        )
        for stream, command, expected_stdout, expected_stderr in cases:
            with self.subTest(stream=stream):
                result = runner._run_check(
                    _check(sys.executable, "-c", command),
                    verbose=False,
                    timeout=0.5,
                )
                self.assertFalse(result.passed)
                self.assertEqual(result.returncode, runner.TIMEOUT_RETURN_CODE)
                self.assertEqual(result.stdout, expected_stdout)
                self.assertIn("timed out", result.stderr)
                if expected_stderr is not None:
                    self.assertIn(expected_stderr, result.stderr)

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

    def test_sdk_probe_also_bounds_its_subprocess(self):
        with mock.patch.object(runner.subprocess, "run") as run:
            run.side_effect = subprocess.TimeoutExpired(cmd=["python"], timeout=1.0)
            self.assertFalse(runner._is_python_sdk_installed())
        self.assertEqual(run.call_args.kwargs["timeout"], runner.PROBE_TIMEOUT_SECONDS)


if __name__ == "__main__":
    unittest.main()
