#!/usr/bin/env python3
"""Regression tests for the vitest bench output parser."""
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

PARSER = Path(__file__).with_name('parse-vitest.py')

PASS_ROW = ('   · emit cycle  1,234,567.89  0.0001  0.2772  0.0008  '
            '0.0009  0.0010  0.0011  0.0012  ±0.21%  5658834')
OTHER_PASS_ROW = ('   · encode cycle  987,654.32  0.0002  0.1999  0.0015  '
                  '0.0016  0.0017  0.0018  0.0019  ±0.30%  1234567')
FAIL_ROW = '   × broken cycle'


class ParseVitestTests(unittest.TestCase):
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
        self.assertEqual(data['summary'], {'total': 2, 'passed': 2, 'failed': 0})

    def test_failure_marker_is_reported_instead_of_fabricated_pass(self):
        # Before the fix these rows were dropped and summary.failed was always 0.
        data = self.parse('\n'.join([PASS_ROW, FAIL_ROW]))
        self.assertEqual(data['summary'], {'total': 2, 'passed': 1, 'failed': 1})
        failed = [r for r in data['results'] if not r['pass']]
        self.assertEqual(len(failed), 1)
        self.assertEqual(failed[0]['name'], 'broken cycle')

    def test_fail_banner_is_reported_when_no_metrics_row_exists(self):
        data = self.parse(' FAIL  sdk.bench.ts > init failed\nError: boom\n')
        self.assertEqual(data['summary'], {'total': 1, 'passed': 0, 'failed': 1})
        self.assertEqual(data['results'][0]['name'], 'init failed')
        self.assertFalse(data['results'][0]['pass'])

    def test_legacy_latin1_capture_is_still_parsed(self):
        data = self.parse('\n'.join([PASS_ROW, FAIL_ROW]), encoding='latin-1')
        self.assertEqual(data['summary'], {'total': 2, 'passed': 1, 'failed': 1})


if __name__ == '__main__':
    unittest.main()
