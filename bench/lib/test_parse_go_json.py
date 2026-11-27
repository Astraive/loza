#!/usr/bin/env python3
"""Regression tests for go test -json benchmark parsing."""
import json
import subprocess
import sys
import unittest
from pathlib import Path

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


if __name__ == "__main__":
    unittest.main()
