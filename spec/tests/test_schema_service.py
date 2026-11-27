import http.client
import json
import os
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

from services.schema_service import SchemaServiceHandler, _env_int


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



class TestSchemaServiceDockerHealthcheck(unittest.TestCase):
    def test_image_port_and_default_healthcheck_match_service(self):
        dockerfile = spec_dir / "docker" / "schema-service" / "Dockerfile"
        dockerfile_lines = dockerfile.read_text(encoding="utf-8").splitlines()
        image_port = int(
            next(
                line.split("=", 1)[1]
                for line in dockerfile_lines
                if line.startswith("ENV SCHEMA_SERVICE_PORT=")
            )
        )
        exposed_port = int(
            next(
                line.split()[1]
                for line in dockerfile_lines
                if line.startswith("EXPOSE ")
            )
        )
        command = json.loads(
            next(
                line.removeprefix("CMD ").strip()
                for line in dockerfile_lines
                if line.startswith("CMD ")
            )
        )
        self.assertEqual(exposed_port, image_port)
        self.assertNotIn("--port", command)

        healthcheck_dir = spec_dir / "docker" / "schema-service"
        sys.path.insert(0, str(healthcheck_dir))
        try:
            import healthcheck

            with patch.dict(
                os.environ, {"SCHEMA_SERVICE_PORT": str(image_port)}, clear=True
            ):
                self.assertEqual(
                    _env_int("SCHEMA_SERVICE_PORT", 9315), image_port
                )
                with patch("healthcheck.urllib.request.urlopen") as urlopen:
                    urlopen.return_value.__enter__.return_value.status = 200
                    self.assertEqual(healthcheck.main(), 0)
                    urlopen.assert_called_once_with(
                        f"http://127.0.0.1:{image_port}/health", timeout=2
                    )

            with patch.dict(os.environ, {}, clear=True):
                with patch("healthcheck.urllib.request.urlopen") as urlopen:
                    urlopen.return_value.__enter__.return_value.status = 200
                    self.assertEqual(healthcheck.main(), 0)
                    urlopen.assert_called_once_with(
                        "http://127.0.0.1:9315/health", timeout=2
                    )
        finally:
            sys.path.remove(str(healthcheck_dir))

if __name__ == "__main__":
    unittest.main()
