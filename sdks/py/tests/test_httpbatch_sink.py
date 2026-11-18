from __future__ import annotations

import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest

from loza.core.http_client import CollectorClient
from loza.sinks.httpbatch import HTTPBatchSink

_EVENT = (
    '{"timestamp":"2025-01-01T00:00:00Z","schema_version":"v1","event_version":"v1",'
    '"event_id":"e","service":"s","event":"x","kind":"event"}'
)


class _Handler(BaseHTTPRequestHandler):
    response_status = 202
    response_body = b'{"status":"accepted","accepted":1}'

    def do_POST(self) -> None:  # noqa: N802
        length = int(self.headers.get("Content-Length", "0"))
        _ = self.rfile.read(length)
        self.send_response(self.response_status)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(self.response_body)

    def log_message(self, fmt: str, *args) -> None:  # noqa: A003
        return None


def _serve(status: int, payload: dict) -> tuple[ThreadingHTTPServer, str]:
    class Handler(_Handler):
        response_status = status
        response_body = json.dumps(payload).encode("utf-8")

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, f"http://127.0.0.1:{server.server_address[1]}"


def test_httpbatch_sink_accepts_clean_batch() -> None:
    server, url = _serve(202, {"status": "accepted", "accepted": 1})
    try:
        sink = HTTPBatchSink(url, service="checkout")
        sink.write(
            '{"schema_version":"v1","event_version":"v1","timestamp":"2026-05-12T00:00:00Z","event_id":"evt_1","service":"checkout","event":"payment.completed","kind":"http"}'
        )
    finally:
        server.shutdown()
        server.server_close()


def test_httpbatch_sink_fails_on_partial_invalid_batch() -> None:
    server, url = _serve(
        207,
        {
            "status": "partial",
            "accepted": 1,
            "invalid": 1,
            "acks": [
                {"event_id": "evt_1", "status": "accepted", "reason": "accepted"},
                {
                    "event_id": "evt_2",
                    "status": "invalid",
                    "reason": "schema_invalid",
                    "message": "schema validation failed",
                },
            ],
        },
    )
    try:
        sink = HTTPBatchSink(url, service="checkout")
        with pytest.raises(RuntimeError, match="schema_invalid"):
            sink.write_batch(
                [
                    '{"schema_version":"v1","event_version":"v1","timestamp":"2026-05-12T00:00:00Z","event_id":"evt_1","service":"checkout","event":"payment.completed","kind":"http"}',
                    '{"schema_version":"v1","event_version":"v1","timestamp":"2026-05-12T00:00:01Z","event_id":"evt_2","service":"checkout","event":"payment.completed","kind":"http"}',
                ]
            )
    finally:
        server.shutdown()
        server.server_close()


@pytest.mark.parametrize("sender", ["sink", "collector_client"])
def test_ingest_does_not_forward_credentials_on_cross_origin_redirect(sender: str) -> None:
    seen: list[tuple[str, str | None, bytes]] = []

    class _Attacker(_Handler):
        def _record(self) -> None:  # noqa: N802
            length = int(self.headers.get("Content-Length", "0"))
            seen.append((self.path, self.headers.get("Authorization"), self.rfile.read(length)))
            self.send_response(202)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"status":"accepted","accepted":1}')

        do_GET = _record
        do_POST = _record

    attacker = ThreadingHTTPServer(("127.0.0.1", 0), _Attacker)
    threading.Thread(target=attacker.serve_forever, daemon=True).start()
    redirect_to = f"http://127.0.0.1:{attacker.server_address[1]}/steal"

    class _RedirectingCollector(_Handler):
        def do_POST(self) -> None:  # noqa: N802
            _ = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            self.send_response(302)
            self.send_header("Location", redirect_to)
            self.send_header("Content-Length", "0")
            self.end_headers()

    collector = ThreadingHTTPServer(("127.0.0.1", 0), _RedirectingCollector)
    threading.Thread(target=collector.serve_forever, daemon=True).start()
    endpoint = f"http://127.0.0.1:{collector.server_address[1]}/ingest"
    try:
        if sender == "sink":
            HTTPBatchSink(endpoint, api_key="super-secret", retries=0, enable_compression=False).write(_EVENT)
        else:
            CollectorClient(endpoint, api_key="super-secret", retries=0).send_batch([_EVENT])
    except RuntimeError:
        pass  # the redirect is refused and surfaced as a failed send
    else:
        pytest.fail("redirected request unexpectedly succeeded")
    finally:
        collector.shutdown()
        collector.server_close()
        attacker.shutdown()
        attacker.server_close()

    assert seen == [], f"credentials/body leaked to redirect target: {seen}"


def test_httpbatch_sink_follows_validated_same_origin_redirect() -> None:
    received: list[str] = []

    class _Collector(_Handler):
        def do_POST(self) -> None:  # noqa: N802
            _ = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            self.send_response(302)
            self.send_header("Location", f"http://127.0.0.1:{self.server.server_address[1]}/final")
            self.send_header("Content-Length", "0")
            self.end_headers()

        def do_GET(self) -> None:  # noqa: N802
            received.append(self.headers.get("Authorization", ""))
            self.send_response(202)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"status":"accepted","accepted":1}')

    server = ThreadingHTTPServer(("127.0.0.1", 0), _Collector)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        sink = HTTPBatchSink(
            f"http://127.0.0.1:{server.server_address[1]}/ingest",
            api_key="same-origin-key",
            retries=0,
            enable_compression=False,
        )
        sink.write(_EVENT)
    finally:
        server.shutdown()
        server.server_close()

    assert received == ["Bearer same-origin-key"]
