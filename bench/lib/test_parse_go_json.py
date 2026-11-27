#!/usr/bin/env python3
"""Regression tests for go test -json benchmark parsing."""
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

BENCH_DIR = Path(__file__).resolve().parents[1]

PARSER = Path(__file__).with_name("parse-go-json.py")


class FakeGoProducer:
    """Emit the JSON event stream produced by go test -json."""

    def __init__(self, benchmark_output):
        self.benchmark_output = benchmark_output

    def output(self):
        return json.dumps({"Action": "output", "Output": self.benchmark_output}) + "\n"


class ParseGoJsonTests(unittest.TestCase):
    def parse(self, output):
        producer = FakeGoProducer(output)
        return subprocess.run(
            [sys.executable, str(PARSER), "test-suite", "component", "timestamp"],
            input=producer.output(), capture_output=True, text=True, check=False,
        )

    def test_short_benchmark_without_memory_statistics(self):
        proc = self.parse("BenchmarkShort-8    1    42 ns/op\n")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        row = json.loads(proc.stdout)["results"][0]
        self.assertEqual(row, {
            "name": "BenchmarkShort-8", "iterations": 1,
            "ns_per_op": 42.0, "pass": True,
        })

    def test_benchmark_with_memory_statistics(self):
        proc = self.parse("BenchmarkMemory-8 200 123.4 ns/op 2048 B/op 7 allocs/op\n")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        row = json.loads(proc.stdout)["results"][0]
        self.assertEqual(row["ns_per_op"], 123.4)
        self.assertEqual(row["bytes_per_op"], 2048)
        self.assertEqual(row["allocs_per_op"], 7)

    def test_unexpected_benchmark_format_is_reported(self):
        proc = self.parse("BenchmarkBroken-8 1 42 ns/op 2048 bytes 7 allocs/op\n")
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("Go benchmark parse error", proc.stderr)

    def test_empty_benchmark_stream_is_not_success(self):
        proc = subprocess.run(
            [sys.executable, str(PARSER), "test-suite", "component", "timestamp"],
            input="{\"Action\":\"pass\"}\n", capture_output=True, text=True,
            check=False,
        )
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("no benchmark result rows", proc.stderr)


    def test_collector_load_without_matching_benchmark_is_skipped(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            bench_dir = root / "bench"
            lib_dir = bench_dir / "lib"
            lib_dir.mkdir(parents=True)
            (root / "collector").mkdir()
            (bench_dir / "run-collector.sh").write_text(
                (BENCH_DIR / "run-collector.sh").read_text(encoding="utf-8"),
                encoding="utf-8",
            )
            (lib_dir / "parse-go-json.py").write_text(
                (BENCH_DIR / "lib" / "parse-go-json.py").read_text(encoding="utf-8"),
                encoding="utf-8",
            )
            (lib_dir / "json-reporter.sh").write_text(
                (BENCH_DIR / "lib" / "json-reporter.sh").read_text(encoding="utf-8"),
                encoding="utf-8",
            )
            bin_dir = root / "bin"
            bin_dir.mkdir()
            fake_go = bin_dir / "go"
            fake_go.write_text(
                r"""#!/usr/bin/env bash
case "$*" in
    *-bench=BenchmarkLoad*)
        printf '%s\n' '{"Action":"pass"}'
        ;;
    *)
        printf '%s\n' '{"Action":"output","Output":"BenchmarkFake-1 1 42 ns/op 8 B/op 1 allocs/op\n"}' '{"Action":"pass"}'
        ;;
esac
""",
                encoding="utf-8",
            )
            fake_go.chmod(0o755)
            env = os.environ.copy()
            env["PATH"] = f"{bin_dir}{os.pathsep}{env['PATH']}"
            proc = subprocess.run(
                ["bash", str(bench_dir / "run-collector.sh")],
                capture_output=True,
                check=False,
                env=env,
                text=True,
                timeout=10,
            )

            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertIn("PASS: collector-duckdb", proc.stdout)
            self.assertIn("PASS: collector-projection", proc.stdout)
            self.assertIn("SKIP: collector-load (no load benchmark found)", proc.stdout)
            load_reports = list((bench_dir / "results").glob("collector-load-*.json"))
            self.assertEqual(len(load_reports), 1)
            self.assertEqual(json.loads(load_reports[0].read_text())["results"], [])

if __name__ == "__main__":
    unittest.main()
