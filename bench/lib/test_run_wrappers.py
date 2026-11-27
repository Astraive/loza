#!/usr/bin/env python3
"""Regression tests for benchmark wrapper exit-status propagation."""
import os
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

BENCH_DIR = Path(__file__).resolve().parents[1]
REPO_ROOT = BENCH_DIR.parent
COPIED_FILES = (
    "bench/run-sdk.sh",
    "bench/run-collector.sh",
    "bench/lib/json-reporter.sh",
    "bench/lib/parse-go-json.py",
    "bench/lib/parse-vitest.py",
)


class BenchmarkWrapperStatusTests(unittest.TestCase):
    def make_fixture(self, root, mode):
        for relative_path in COPIED_FILES:
            source = REPO_ROOT / relative_path
            destination = root / relative_path
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, destination)

        for relative_path in (
            "sdks/go/bench",
            "sdks/py",
            "sdks/rs",
            "sdks/js",
            "collector",
            "bin",
        ):
            (root / relative_path).mkdir(parents=True, exist_ok=True)
        (root / "sdks/js/package.json").write_text("{}\n", encoding="utf-8")

        timestamp = f"test-{os.getpid()}-{time.time_ns()}"
        commands = {
            "date": '#!/usr/bin/env bash\nprintf \'%s\\n\' "$AUDIT_TIMESTAMP"\n',
            "go": r'''#!/usr/bin/env bash
if [ "$AUDIT_MODE" = failure ]; then
    exit 7
fi
case "$*" in
    *-bench=BenchmarkLoad*)
        printf '%s\n' '{"Action":"pass"}'
        ;;
    *)
        printf '%s\n' '{"Action":"output","Output":"BenchmarkTiny-1 1 123 ns/op\n"}' '{"Action":"pass"}'
        ;;
esac
''',
            "python3": r'''#!/usr/bin/env bash
case "$1" in
    -c|-|*/parse-go-json.py|*/parse-vitest.py)
        exec "$REAL_PYTHON" "$@"
        ;;
    *)
        exit 7
        ;;
esac
''',
            "cargo": "#!/usr/bin/env bash\nexit 7\n",
            "bun": "#!/usr/bin/env bash\necho 'simulated benchmark failure'\nexit 7\n",
        }
        bin_dir = root / "bin"
        for name, content in commands.items():
            executable = bin_dir / name
            executable.write_text(content, encoding="utf-8")
            executable.chmod(0o755)

        env = os.environ.copy()
        env.update({
            "PATH": f"{bin_dir}{os.pathsep}{env['PATH']}",
            "REAL_PYTHON": sys.executable,
            "AUDIT_MODE": mode,
            "AUDIT_TIMESTAMP": timestamp,
        })
        return env, timestamp

    def run_wrapper(self, root, env, script, *args):
        try:
            return subprocess.run(
                ["bash", str(root / script), *args],
                cwd=root,
                env=env,
                capture_output=True,
                check=False,
                text=True,
                timeout=10,
            )
        finally:
            vitest_output = Path(f"/tmp/vitest_bench_{env['AUDIT_TIMESTAMP']}.txt")
            if vitest_output.exists():
                vitest_output.unlink()

    def test_sdk_all_returns_failure_when_any_child_fails(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            env, _ = self.make_fixture(root, "failure")

            proc = self.run_wrapper(root, env, "bench/run-sdk.sh", "--sdk", "all")

            self.assertNotEqual(proc.returncode, 0, proc.stdout)
            for suite in ("sdk-go", "sdk-py", "sdk-rs", "sdk-js"):
                self.assertIn(f"FAIL: {suite}", proc.stdout)

    def test_collector_returns_failure_when_children_fail(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            env, _ = self.make_fixture(root, "failure")

            proc = self.run_wrapper(root, env, "bench/run-collector.sh")

            self.assertNotEqual(proc.returncode, 0, proc.stdout)
            for suite in ("collector-duckdb", "collector-projection", "collector-load"):
                self.assertIn(f"FAIL: {suite}", proc.stdout)

    def test_successful_go_run_and_missing_optional_load_remain_successful(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            env, _ = self.make_fixture(root, "success")

            sdk = self.run_wrapper(root, env, "bench/run-sdk.sh", "--sdk", "go")
            collector = self.run_wrapper(root, env, "bench/run-collector.sh")

            self.assertEqual(sdk.returncode, 0, sdk.stderr)
            self.assertIn("PASS: sdk-go", sdk.stdout)
            self.assertEqual(collector.returncode, 0, collector.stderr)
            self.assertIn("PASS: collector-duckdb", collector.stdout)
            self.assertIn("PASS: collector-projection", collector.stdout)
            self.assertIn("SKIP: collector-load (no load benchmark found)", collector.stdout)


if __name__ == "__main__":
    unittest.main()
