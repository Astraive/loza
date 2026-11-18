import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

repo_root = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(repo_root / "spec"))

from services.schema_service import SchemaServiceHandler


class TestSchemaServicePathContainment(unittest.TestCase):
    """GET /schemas/<name> must never escape the spec/schemas/json directory."""

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)
        self.base = self.root / "spec" / "schemas" / "json"
        self.base.mkdir(parents=True)
        (self.base / "event.schema.json").write_text("{}", encoding="utf-8")

        # A sibling directory whose name merely shares the base's prefix. This
        # is the exact case that str.startswith() cannot distinguish from a real
        # child of the json/ directory.
        sibling = self.root / "spec" / "schemas" / "json_evil"
        sibling.mkdir()
        (sibling / "foo.json").write_text('"leaked"', encoding="utf-8")

        self.handler = SchemaServiceHandler.__new__(SchemaServiceHandler)
        self.handler.server = type("FakeServer", (), {})()
        self.handler.headers = {}

    def tearDown(self):
        self._tmp.cleanup()

    def _get(self, path):
        """Drive do_GET without a real socket, capturing its terminal action."""
        with patch.object(SchemaServiceHandler, "_spec_root", return_value=self.root), \
             patch.object(SchemaServiceHandler, "send_error") as err, \
             patch.object(SchemaServiceHandler, "_send_file") as send:
            self.handler.path = path
            self.handler.do_GET()
        return err, send

    def test_sibling_directory_sharing_base_prefix_is_forbidden(self):
        # Resolves to <root>/spec/schemas/json_evil/foo.json, NOT to a file
        # under spec/schemas/json/. startswith() accepts it; it must be 403.
        err, send = self._get("/schemas/../json_evil/foo.json")
        err.assert_called_once()
        self.assertEqual(err.call_args.args[0], 403)
        send.assert_not_called()

    def test_legitimate_schema_is_served_from_base(self):
        err, send = self._get("/schemas/event.schema.json")
        err.assert_not_called()
        send.assert_called_once()
        self.assertTrue(send.call_args.args[0].is_relative_to(self.base))


if __name__ == "__main__":
    unittest.main()
