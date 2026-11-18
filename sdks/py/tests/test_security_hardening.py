import pytest

import loza
from loza.core.client import Logger as ClientLogger
from loza.core.config import Config
from loza.core.http_client import CollectorClient
from loza.core.logger import Logger
from loza.cortex.client import CortexClient
from loza.core.pipeline import DiskOfflineBuffer
from loza.sinks.file import FileSink
from loza.sinks.httpbatch import HTTPBatchSink


def _attr_leaf_count(bucket):
    """Independent flattened leaf count used to verify the limit enforcement."""
    total = 0
    for value in bucket.values():
        if isinstance(value, dict) and value:
            total += _attr_leaf_count(value)
        else:
            total += 1
    return total


def _assert_nested_dotted_attrs_respect_limit(logger_cls) -> None:
    max_attrs = 5
    cfg = loza.Config.test("nested-limit").with_sink(loza.MemorySink())
    cfg.security = loza.SecurityConfig(max_attr_count=max_attrs)
    logger = logger_cls(cfg)
    ctx = logger.start_event(loza.Params(event="nested"))
    # All these attributes collapse into one top-level "payload" key once the
    # dotted keys are expanded by _set_path, so counting top-level keys misses them.
    for i in range(20):
        logger.enrich(ctx, **{f"payload.field{i}": i})

    logger._enforce_attr_limit(ctx)

    buckets = (ctx.attrs, ctx.user, ctx.tenant, ctx.resource, ctx.http)
    # The _truncated marker is added after enforcement and counts as one attribute.
    assert sum(_attr_leaf_count(bucket) for bucket in buckets) <= max_attrs + 1
    assert ctx.attrs.get("_truncated") is True
    assert len(ctx.attrs["payload"]) <= max_attrs


def test_collector_client_rejects_metadata_endpoint():
    with pytest.raises(ValueError, match="non-public"):
        CollectorClient("http://169.254.169.254/latest/meta-data")


@pytest.mark.parametrize("auth_header", ["Authorization", "X-API-Key"])
def test_api_key_over_cleartext_http_rejected(auth_header):
    """An api_key must never be POSTed to a non-local plain-HTTP endpoint."""
    with pytest.raises(ValueError, match="HTTPS"):
        CollectorClient("http://collector.example/events", api_key="secret", auth_header=auth_header)


def test_api_key_over_cleartext_http_rejected_by_sink():
    with pytest.raises(ValueError, match="HTTPS"):
        HTTPBatchSink("http://collector.example/events", api_key="secret")


def test_api_key_over_cleartext_http_rejected_by_config():
    with pytest.raises(ValueError, match="HTTPS"):
        Config(collector_endpoint="http://collector.example/events", api_key="secret").validate()


@pytest.mark.parametrize(
    "endpoint",
    ["https://collector.example/events", "http://127.0.0.1:9308/events", "http://localhost:9308/events"],
)
def test_api_key_allowed_over_tls_and_local_http(endpoint):
    client = CollectorClient(endpoint, api_key="secret")
    assert client._auth_headers() == {"Authorization": "Bearer secret"}


def test_file_sink_rejects_parent_traversal():
    with pytest.raises(ValueError, match="traversal"):
        FileSink("../loza.ndjson")


def test_disk_offline_buffer_rejects_parent_traversal():
    with pytest.raises(ValueError, match="traversal"):
        DiskOfflineBuffer("../loza-spool.ndjson")


def test_cortex_client_rejects_metadata_endpoint():
    with pytest.raises(ValueError, match="non-public"):
        CortexClient("http://169.254.169.254/latest/meta-data")


def test_config_repr_does_not_leak_api_key() -> None:
    api_key = "sk-loza-super-secret-value"
    cfg = Config(service="svc").with_api_key(api_key).with_basic_auth("secret-user", "secret-pass")

    for rendered in (repr(cfg), str(cfg)):
        assert api_key not in rendered
        assert "secret-user" not in rendered
        assert "secret-pass" not in rendered

    # Redacting the repr must not break access to the configured credentials.
    assert cfg.api_key == api_key
    assert cfg.username == "secret-user"
    assert cfg.password == "secret-pass"
    assert cfg.with_service("other").api_key == api_key


def test_max_attr_count_counts_nested_dotted_keys() -> None:
    _assert_nested_dotted_attrs_respect_limit(Logger)


def test_max_attr_count_counts_nested_dotted_keys_legacy_client() -> None:
    _assert_nested_dotted_attrs_respect_limit(ClientLogger)
