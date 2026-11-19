#!/usr/bin/env python3
"""Regression tests for the vitest bench output parser."""
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

PARSER = Path(__file__).with_name('parse-vitest.py')

# Fixtures from main (dedup / failure handling / UTF-8 + latin-1 decoding).
PASS_ROW = ('   · emit cycle  1,234,567.89  0.0001  0.2772  0.0008  '
            '0.0009  0.0010  0.0011  0.0012  ±0.21%  5658834')
OTHER_PASS_ROW = ('   · encode cycle  987,654.32  0.0002  0.1999  0.0015  '
                  '0.0016  0.0017  0.0018  0.0019  ±0.30%  1234567')
FAIL_ROW = '   × broken cycle'

# A representative vitest/tinybench result row. The marker is U+00B7 (MIDDLE DOT),
# which vitest emits as the two UTF-8 bytes C2 B7.
UTF8_ROW = '\u00b7 emit cycle    1234567    100    200    0.83    100\n'

# Fixtures from PR #1364 (comma-formatted numerics and skipped-row warnings).
# A vitest bench row uses two or more spaces between columns. Column layout:
# name, hz, min, max, mean, p75, p99, p995, p999, rme, samples
COMMA_MEAN_ROW = (
    "\u00b7 encode    1,234.56    0.50    0.90    1,000.5    "
    "1.10    1.20    1.30    1.40    1.50    10"
)
PLAIN_ROW = (
    "\u00b7 decode    2000    0.50    0.90    2.5    "
    "3.10    3.20    3.30    3.40    3.50    10"
)
# Newer vitest can append a tolerance to the mean cell, which is not a float.
TOLERANCE_ROW = (
    "\u00b7 sample    3000    0.50    0.90    4.5\u00b11.2%    "
    "5.10    5.20    5.30    5.40    5.50    10"
)


def run_parser(rows):
    """Run the parser on the given rows and return (completed_process, payload)."""
    with tempfile.NamedTemporaryFile(
        "w", suffix=".txt", delete=False, encoding="latin-1"
    ) as fh:
        fh.write("\n".join(rows) + "\n")
        path = fh.name
    try:
        proc = subprocess.run(
            [sys.executable, str(PARSER), path, "sdk-js", "TS"],
            capture_output=True,
            text=True,
            timeout=30,
        )
    finally:
        Path(path).unlink(missing_ok=True)
    payload = json.loads(proc.stdout) if proc.returncode == 0 else None
    return proc, payload


class ParseVitestTests(unittest.TestCase):
    """Regression tests added on main (dedup, failure handling, latin-1 fallback)."""

    def parse(self, text, encoding='utf-8'):
        with tempfile.TemporaryDirectory() as tmp:
            src = Path(tmp) / 'vitest.txt'
            out = Path(tmp) / 'result.json'
            src.write_bytes(text.encode(encoding))
            subprocess.run(
                [sys.executable, str(PARSER), str(src), 'sdk-js', 'TS', str(out)],
                check=True, capture_output=True,
            )
            return json.loads(out.read_text())

    def test_passing_benchmarks_get_real_counts_and_no_duplicate_rows(self):
        # The reporter prints a benchmark row more than once; it must be
        # counted once, with metrics parsed from the UTF-8 output.
        data = self.parse('\n'.join([PASS_ROW, PASS_ROW, OTHER_PASS_ROW]))
        self.assertEqual(len(data['results']), 2)
        self.assertTrue(all(r['pass'] for r in data['results']))
        self.assertEqual(data['results'][0]['hz'], 1234567.89)
        self.assertEqual(data['results'][0]['ns_per_op'], 800.0)
        self.assertEqual(data['summary'], {'total': 2, 'passed': 2, 'failed': 0, 'skipped': 0})

    def test_failure_marker_is_reported_instead_of_fabricated_pass(self):
        # Before the fix these rows were dropped and summary.failed was always 0.
        data = self.parse('\n'.join([PASS_ROW, FAIL_ROW]))
        self.assertEqual(data['summary'], {'total': 2, 'passed': 1, 'failed': 1, 'skipped': 0})
        failed = [r for r in data['results'] if not r['pass']]
        self.assertEqual(len(failed), 1)
        self.assertEqual(failed[0]['name'], 'broken cycle')

    def test_fail_banner_is_reported_when_no_metrics_row_exists(self):
        data = self.parse(' FAIL  sdk.bench.ts > init failed\nError: boom\n')
        self.assertEqual(data['summary'], {'total': 1, 'passed': 0, 'failed': 1, 'skipped': 0})
        self.assertEqual(data['results'][0]['name'], 'init failed')
        self.assertFalse(data['results'][0]['pass'])

    def test_legacy_latin1_capture_is_still_parsed(self):
        data = self.parse('\n'.join([PASS_ROW, FAIL_ROW]), encoding='latin-1')
        self.assertEqual(data['summary'], {'total': 2, 'passed': 1, 'failed': 1, 'skipped': 0})


class ParseVitestTest(unittest.TestCase):
    """Regression tests from PR #1030 — UTF-8 decoding fix for #97."""

    def run_parser(self, rows):
        """Write rows to a UTF-8 file and parse it, returning the parsed JSON."""
        with tempfile.NamedTemporaryFile(mode='wb', suffix='.txt', delete=False) as fh:
            fh.write(rows.encode('utf-8'))
            path = fh.name
        try:
            proc = subprocess.run(
                [sys.executable, str(PARSER), path, 'sdk-js', 'ts'],
                capture_output=True, text=True,
            )
            assert proc.returncode == 0, proc.stderr
            return json.loads(proc.stdout)
        finally:
            os.unlink(path)

    def test_utf8_middle_dot_rows_are_parsed(self):
        """UTF-8 encoded U+00B7 markers must be recognized (regression for #97)."""
        output = self.run_parser(UTF8_ROW)
        self.assertEqual(output['summary']['total'], 1)
        self.assertEqual(len(output['results']), 1)
        result = output['results'][0]
        self.assertEqual(result['name'], 'emit cycle')
        self.assertEqual(result['hz'], 1234567.0)
        self.assertEqual(result['ns_per_op'], 830000.0)
        self.assertTrue(result['pass'])

    def test_non_marker_lines_are_ignored(self):
        output = self.run_parser('some unrelated log line\n' + UTF8_ROW)
        self.assertEqual(output['summary']['total'], 1)

    def test_empty_input_yields_no_results(self):
        output = self.run_parser('')
        self.assertEqual(output['summary']['total'], 0)
        self.assertEqual(output['results'], [])


class ParseVitestCommaTests(unittest.TestCase):
    """Regression tests from PR #1364 — comma numerics and skipped-row warnings."""

    def test_comma_formatted_numeric_columns_are_parsed(self):
        """A comma-formatted hz/mean must not be silently dropped."""
        proc, payload = run_parser([COMMA_MEAN_ROW])
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(proc.stderr, "")
        self.assertEqual(payload["summary"]["skipped"], 0)
        self.assertEqual(len(payload["results"]), 1)
        result = payload["results"][0]
        self.assertEqual(result["name"], "encode")
        self.assertAlmostEqual(result["hz"], 1234.56)
        # 1,000.5 * 1_000_000 == 1_000_500_000 ns/op
        self.assertAlmostEqual(result["ns_per_op"], 1_000_500_000.0)

    def test_plain_numeric_columns_still_parse(self):
        """Unformatted numbers must keep working (no regression)."""
        proc, payload = run_parser([PLAIN_ROW])
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(payload["summary"]["skipped"], 0)
        self.assertEqual(len(payload["results"]), 1)
        self.assertAlmostEqual(payload["results"][0]["ns_per_op"], 2_500_000.0)

    def test_malformed_numeric_column_is_skipped_with_warning(self):
        """An unparseable mean is skipped, warned about, and counted."""
        proc, payload = run_parser([PLAIN_ROW, TOLERANCE_ROW])
        self.assertEqual(proc.returncode, 0, proc.stderr)
        # The valid row survives; the malformed one is dropped.
        self.assertEqual(len(payload["results"]), 1)
        self.assertEqual(payload["results"][0]["name"], "decode")
        # Partial results are detectable in both the JSON summary and stderr.
        self.assertEqual(payload["summary"]["skipped"], 1)
        self.assertIn("warning:", proc.stderr)
        self.assertIn("line 2", proc.stderr)

    def test_all_malformed_rows_yield_empty_results_not_a_crash(self):
        proc, payload = run_parser([TOLERANCE_ROW])
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(payload["results"], [])
        self.assertEqual(payload["summary"]["total"], 0)
        self.assertEqual(payload["summary"]["skipped"], 1)
        self.assertIn("warning:", proc.stderr)


if __name__ == '__main__':
    unittest.main()
