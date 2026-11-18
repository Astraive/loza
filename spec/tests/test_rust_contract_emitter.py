"""Regression tests for the Rust contract emitter.

``CONTRACT_JSON`` is emitted as a Rust raw string, so the JSON must be
inserted verbatim (no backslash escaping of quotes/backslashes) and the
raw-string delimiter must be chosen so the payload cannot close it early.
"""

import json
import re
import sys
import unittest
from pathlib import Path

repo_root = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(repo_root / "spec" / "codegen"))

from emitters.rust import render_rust_contract  # noqa: E402

CONTRACT = {
    "product_version": "0.3.4",
    "spec_version": "v1",
    "api_version": "v1",
    "event_version": "v1",
    # Quotes and backslashes are legal JSON string content and must survive
    # verbatim inside a Rust raw string.
    "tricky": 'a quote " and a backslash \\ and a hash #',
    # A quote immediately followed by '#' would close an r#"..."# literal.
    "terminator_like": 'value "# not a real terminator',
    "limits": {"max_event_size_bytes": 65536},
}

RAW_CONST_RE = re.compile(
    r'pub const CONTRACT_JSON: &str = r(?P<h>#+)"(?P<body>.*?)"(?P=h);',
    re.DOTALL,
)


class RustContractEmitterTests(unittest.TestCase):
    def _extract(self, output):
        match = RAW_CONST_RE.search(output)
        self.assertIsNotNone(match, "CONTRACT_JSON raw string literal not found")
        return match.group("h"), match.group("body")

    def test_contract_json_is_valid_json(self):
        output = render_rust_contract(CONTRACT)
        _, body = self._extract(output)

        self.assertTrue(body.startswith('{\n  "product_version"'))
        self.assertEqual(json.loads(body), CONTRACT)

    def test_raw_string_delimiter_is_safe(self):
        output = render_rust_contract(CONTRACT)
        hashes, body = self._extract(output)

        self.assertNotIn('"' + hashes, body)
        # The payload contains a `"#` sequence, so a single-'#' delimiter
        # (r#"..."#) would be terminated early; the emitter must bump it.
        self.assertGreaterEqual(len(hashes), 2)


if __name__ == "__main__":
    unittest.main()
