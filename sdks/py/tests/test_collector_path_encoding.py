from __future__ import annotations

import pytest

from loza.core import http_client
from loza.core.http_client import CollectorClient, _collector_path

BASE = "http://127.0.0.1:9999"

# Contains a slash, query delimiter, fragment delimiter, and traversal
# components. After encoding it must remain a single opaque path segment.
HOSTILE_ID = "../../secret?x=1#frag"
HOSTILE_ENCODED = "..%2F..%2Fsecret%3Fx%3D1%23frag"


class _RecordingResponse:
    def __enter__(self) -> _RecordingResponse:
        return self

    def __exit__(self, *_args: object) -> None:
        return None

    def read(self, *_args: object) -> bytes:
        return b"{}"


@pytest.fixture
def recorded(monkeypatch):
    urls: list[str] = []

    def fake_urlopen(req, *_args, **_kwargs):
        urls.append(req.full_url)
        return _RecordingResponse()

    monkeypatch.setattr(http_client, "urlopen", fake_urlopen)
    return urls


@pytest.fixture
def client() -> CollectorClient:
    # A loopback endpoint avoids DNS resolution during validation.
    return CollectorClient(BASE + "/events", retries=0)


@pytest.mark.parametrize(
    "segments, expected",
    [
        (("events", "abc"), "/events/abc"),
        (("events", HOSTILE_ID), "/events/" + HOSTILE_ENCODED),
        (("events", ".."), "/events/%2E%2E"),
        (("events", "."), "/events/%2E"),
        (("dlq", "id with space", "replay"), "/dlq/id%20with%20space/replay"),
        (("keys", "a/b?c#d&e"), "/keys/a%2Fb%3Fc%23d%26e"),
    ],
)
def test_collector_path_encodes_segments(segments, expected):
    path = _collector_path(*segments)
    assert path == expected
    assert "?" not in path and "#" not in path


@pytest.mark.parametrize(
    "call, expected_path",
    [
        (lambda c: c.delete(event_id=HOSTILE_ID), "/events/" + HOSTILE_ENCODED),
        (lambda c: c.delete(id=HOSTILE_ID), "/events/" + HOSTILE_ENCODED),
        (lambda c: c.delete(tenant_id=HOSTILE_ID), "/events/by-tenant/" + HOSTILE_ENCODED),
        (lambda c: c.delete(user_id=HOSTILE_ID), "/events/by-user/" + HOSTILE_ENCODED),
        (lambda c: c.dlq_read(HOSTILE_ID), "/dlq/" + HOSTILE_ENCODED),
        (lambda c: c.dlq_replay(HOSTILE_ID), "/dlq/" + HOSTILE_ENCODED + "/replay"),
        (lambda c: c.keys_revoke(HOSTILE_ID), "/keys/" + HOSTILE_ENCODED),
        (lambda c: c.keys_rotate(HOSTILE_ID), "/keys/" + HOSTILE_ENCODED + "/rotate"),
        (lambda c: c.sinks_test(HOSTILE_ID), "/sinks/" + HOSTILE_ENCODED + "/test"),
    ],
)
def test_control_plane_ids_are_percent_encoded(client, recorded, call, expected_path):
    call(client)
    assert recorded == [BASE + expected_path]


def test_traversal_id_cannot_change_request_target(client, recorded):
    client.delete(event_id="..")
    client.dlq_read("..")
    assert recorded == [
        BASE + "/events/%2E%2E",
        BASE + "/dlq/%2E%2E",
    ]
