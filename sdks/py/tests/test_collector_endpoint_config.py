import pytest

from loza.core.client import Logger as ClientLogger
from loza.core.config import Config
from loza.core.http_client import CollectorClient
from loza.core.logger import Logger
from loza.sinks.httpbatch import HTTPBatchSink


@pytest.mark.parametrize("logger_type", [Logger, ClientLogger])
def test_collector_base_url_targets_ingest_route(logger_type):
    logger = logger_type(Config.production("checkout").with_collector_endpoint("https://collector.example/"))

    sink = next(sink for sink in logger._config.sinks if isinstance(sink, HTTPBatchSink))

    assert sink.endpoint == "https://collector.example/events"


@pytest.mark.parametrize(
    ("dsn", "username", "password", "collector"),
    [
        (
            "loza://private-user:private-secret@collector.example/private-collector",
            "private-user",
            "private-secret",
            "private-collector",
        ),
        (
            "loza://lz_pub_6DJvd3D0izOaQx3n5BhKqN:@collector.example/public-collector",
            "lz_pub_6DJvd3D0izOaQx3n5BhKqN",
            "",
            "public-collector",
        ),
    ],
)
def test_credentialed_dsn_installs_scoped_credential_free_endpoint(dsn, username, password, collector):
    logger = ClientLogger(Config.production("checkout").with_collector_endpoint(dsn))

    sink = next(sink for sink in logger._config.sinks if isinstance(sink, HTTPBatchSink))

    assert logger._config.collector_name == collector
    assert sink.endpoint == f"https://collector.example:443/collectors/{collector}/events"
    assert sink.username == username
    assert sink.password == password
    assert username not in sink.endpoint


def test_api_key_requires_https_for_non_local_endpoints() -> None:
    with pytest.raises(ValueError, match="require HTTPS"):
        Config.dev("checkout").with_collector_endpoint("http://collector.example:80").with_api_key(
            "lz_sec_live_key"
        ).validate()
    with pytest.raises(ValueError, match="require HTTPS"):
        Config.dev("checkout").with_collector_endpoint("loza://collector.example/events?tls=false").with_api_key(
            "lz_sec_live_key"
        ).validate()
    with pytest.raises(ValueError, match="require HTTPS"):
        CollectorClient("http://collector.example/events", api_key="lz_sec_live_key")

    # Loopback remains permitted for local development.
    Config.dev("checkout").with_collector_endpoint("http://localhost:9308").with_api_key("local-key").validate()
    CollectorClient("http://localhost:9308/events", api_key="local-key")


def test_api_key_precedes_public_basic_auth() -> None:
    sink = HTTPBatchSink(
        "https://collector.example/collectors/public-collector/events",
        api_key="api-key",
        username="lz_pub_6DJvd3D0izOaQx3n5BhKqN",
        password="",
    )

    assert sink._auth_headers() == {"Authorization": "Bearer api-key"}


@pytest.mark.parametrize(
    "kwargs",
    [
        {"api_key": "lz_sec_secret"},
        {"username": "basic", "password": "secret"},
    ],
)
def test_credentials_reject_plaintext_http_remote_collector(kwargs) -> None:
    config = Config(collector_endpoint="http://remote.example/events", **kwargs)

    with pytest.raises(ValueError, match="HTTPS"):
        config.validate()


@pytest.mark.parametrize(
    "endpoint",
    [
        "https://remote.example/events",
        "http://localhost:8080/events",
        "http://127.0.0.1:8080/events",
        "http://[::1]:8080/events",
    ],
)
def test_api_key_allows_https_and_local_http_collectors(endpoint) -> None:
    Config(collector_endpoint=endpoint, api_key="lz_sec_secret").validate()


def test_plaintext_http_remote_collector_without_credentials_is_allowed() -> None:
    Config(collector_endpoint="http://remote.example/events").validate()
