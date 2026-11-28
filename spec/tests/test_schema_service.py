import http.client
import shlex
import shutil
import sys
import tempfile
import threading
import time
import unittest
from http.server import ThreadingHTTPServer
from pathlib import Path
from unittest.mock import patch

repo_root = Path(__file__).resolve().parents[2]
spec_dir = repo_root / "spec"
sys.path.insert(0, str(spec_dir))

from services.schema_service import SchemaServiceHandler


class TestSchemaServicePathContainment(unittest.TestCase):
    """GET /schemas/<name> must never escape the spec/schemas/json directory."""

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)
        # _spec_root() resolves to spec/, so the corrected base is <root>/schemas/json.
        self.base = self.root / "schemas" / "json"
        self.base.mkdir(parents=True)
        (self.base / "event.schema.json").write_text("{}", encoding="utf-8")

        # A sibling directory whose name merely shares the base's prefix. This
        # is the exact case that str.startswith() cannot distinguish from a real
        # child of the json/ directory.
        sibling = self.root / "schemas" / "json_evil"
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
        # Resolves to <root>/schemas/json_evil/foo.json, NOT to a file
        # under schemas/json/. startswith() accepts it; it must be 403.
        err, send = self._get("/schemas/../json_evil/foo.json")
        err.assert_called_once()
        self.assertEqual(err.call_args.args[0], 403)
        send.assert_not_called()

    def test_legitimate_schema_is_served_from_base(self):
        err, send = self._get("/schemas/event.schema.json")
        err.assert_not_called()
        send.assert_called_once()
        self.assertTrue(send.call_args.args[0].is_relative_to(self.base))


class TestSchemaServiceSchemas(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), SchemaServiceHandler)
        cls.server.start_time = time.time()
        cls.port = cls.server.server_address[1]
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join(timeout=5)

    def _get(self, path):
        conn = http.client.HTTPConnection("127.0.0.1", self.port, timeout=5)
        try:
            conn.request("GET", path)
            resp = conn.getresponse()
            return resp.status, resp.read()
        finally:
            conn.close()

    def test_schema_file_is_served(self):
        # Regression for double-`spec` base path: /schemas/<name> used to 404
        # because it resolved to spec/spec/schemas/json/<name>.
        status, body = self._get("/schemas/event.schema.json")
        expected = (spec_dir / "schemas" / "json" / "event.schema.json").read_bytes()
        self.assertEqual(status, 200)
        self.assertEqual(body, expected)

    def test_missing_schema_returns_404(self):
        status, _ = self._get("/schemas/does-not-exist.schema.json")
        self.assertEqual(status, 404)

    def test_path_traversal_is_forbidden(self):
        # The containment check must use the same corrected base as the target.
        status, _ = self._get("/schemas/../loza-spec.yaml")
        self.assertEqual(status, 403)


class TestSchemaServiceImageLayout(unittest.TestCase):
    def test_docker_copy_layout_serves_schema_and_contract(self):
        dockerfile = repo_root / "spec" / "docker" / "schema-service" / "Dockerfile"
        copy_instructions = []
        for line in dockerfile.read_text(encoding="utf-8").splitlines():
            if not line.startswith("COPY "):
                continue
            parts = shlex.split(line)
            self.assertEqual(len(parts), 3, f"unsupported COPY instruction: {line}")
            copy_instructions.append((parts[1], parts[2]))
        self.assertTrue(copy_instructions)

        with tempfile.TemporaryDirectory() as tmp:
            image_root = Path(tmp) / "app" / "spec"
            image_root.mkdir(parents=True)
            for source, destination in copy_instructions:
                source_path = repo_root / source
                destination_path = image_root / destination
                if source_path.is_dir():
                    shutil.copytree(source_path, destination_path, dirs_exist_ok=True)
                    continue
                if destination.endswith("/") or destination in (".", "./") or destination_path.is_dir():
                    destination_path /= source_path.name
                destination_path.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(source_path, destination_path)

            with patch.object(SchemaServiceHandler, "_spec_root", return_value=image_root):
                server = ThreadingHTTPServer(("127.0.0.1", 0), SchemaServiceHandler)
                thread = threading.Thread(target=server.serve_forever, daemon=True)
                thread.start()
                try:
                    def get(path):
                        conn = http.client.HTTPConnection("127.0.0.1", server.server_port, timeout=5)
                        try:
                            conn.request("GET", path)
                            response = conn.getresponse()
                            return response.status, response.read()
                        finally:
                            conn.close()

                    schema_status, schema_body = get("/schemas/event.schema.json")
                    contract_status, contract_body = get("/contract")
                finally:
                    server.shutdown()
                    server.server_close()
                    thread.join(timeout=5)

        self.assertEqual(schema_status, 200)
        self.assertEqual(
            schema_body,
            (spec_dir / "schemas" / "json" / "event.schema.json").read_bytes(),
        )
        self.assertEqual(contract_status, 200)
        self.assertEqual(
            contract_body,
            (spec_dir / "generated" / "contract" / "loza-contract.json").read_bytes(),
        )


if __name__ == "__main__":
    unittest.main()
