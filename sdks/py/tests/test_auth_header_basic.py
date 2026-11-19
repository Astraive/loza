from __future__ import annotations

import base64
import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from loza.core.http_client import CollectorClient
from loza.sinks.httpbatch import HTTPBatchSink

_EXPECTED_BASIC = "Basic " + base64.b64encode(b"user:secret").decode("ascii")


def test_httpbatch_basic_auth_honors_custom_auth_header() -> None:
    sink = HTTPBatchSink(
        "https://collector.example/events",
        username="user",
        password="secret",
        auth_header="X-Auth-Token",
    )

    assert sink._auth_headers() == {"X-Auth-Token": _EXPECTED_BASIC}


def test_httpbatch_basic_auth_defaults_to_authorization() -> None:
    sink = HTTPBatchSink(
        "https://collector.example/events",
        username="user",
        password="secret",
    )

    assert sink._auth_headers() == {"Authorization": _EXPECTED_BASIC}


def test_collector_client_basic_auth_honors_custom_auth_header() -> None:
    client = CollectorClient(
        "https://collector.example/events",
        username="user",
        password="secret",
        auth_header="X-Auth-Token",
    )

    assert client._auth_headers() == {"X-Auth-Token": _EXPECTED_BASIC}


def test_httpbatch_sends_basic_auth_under_custom_header() -> None:
    seen: dict[str, str] = {}

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:  # noqa: N802
            length = int(self.headers.get("Content-Length", "0"))
            _ = self.rfile.read(length)
            seen.update(dict(self.headers.items()))
            body = json.dumps({"status": "accepted", "accepted": 1}).encode("utf-8")
            self.send_response(202)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, fmt: str, *args) -> None:  # noqa: A003
            return None

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        sink = HTTPBatchSink(
            f"http://127.0.0.1:{server.server_address[1]}/events",
            username="user",
            password="secret",
            auth_header="X-Auth-Token",
        )
        sink.write_batch(
            [
                '{"schema_version":"v1","event_version":"v1","timestamp":"2026-05-12T00:00:00Z",'
                '"event_id":"evt_1","service":"checkout","event":"payment.completed","kind":"http"}'
            ]
        )
    finally:
        server.shutdown()
        server.server_close()

    assert seen.get("X-Auth-Token") == _EXPECTED_BASIC
    assert "Authorization" not in seen
