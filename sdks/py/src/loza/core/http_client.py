from __future__ import annotations

import base64
import http.client
import json
import socket
import time
import urllib.request
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from ipaddress import ip_address
from os import getenv
from typing import Any, Iterable
from urllib.parse import quote, urlencode, urlparse
from urllib.error import HTTPError, URLError
from urllib.request import HTTPRedirectHandler, Request, build_opener, urlopen
from ..generated.spec_contract import (
    CollectorResponse,
    LOZA_INGEST_API_VERSION,
    build_ingest_envelope,
    parse_collector_response,
    validate_event_payload,
)
from ..version import SDK_VERSION
from .dsn import is_public_dsn_username


class InstrumentedHTTPClient:
    def open(self, request: Request, timeout: float = 5.0):
        return urlopen(request, timeout=timeout)


def _decode_event(encoded: str) -> dict:
    payload = json.loads(encoded)
    if not isinstance(payload, dict):
        raise ValueError("encoded events must decode to JSON objects")
    validate_event_payload(payload, strict=False)
    return payload


def _build_ingest_body(encoded_events: Iterable[str], sdk_name: str, sdk_version: str, service: str) -> bytes:
    events = [_decode_event(encoded) for encoded in encoded_events]
    if not events:
        raise ValueError("collector batch must contain at least one event")
    envelope = build_ingest_envelope(events, sdk_name, sdk_version, service)
    _validate_ingest_envelope(envelope)
    return json.dumps(envelope, separators=(",", ":")).encode("utf-8")


def _parse_collector_response_body(raw: bytes) -> tuple[dict, CollectorResponse]:
    payload: dict = {}
    if raw:
        payload_obj = json.loads(raw.decode("utf-8"))
        if not isinstance(payload_obj, dict):
            raise ValueError("collector response must be a JSON object")
        payload = payload_obj
    return payload, parse_collector_response(payload)


def _collector_response_summary(response: CollectorResponse) -> str:
    return (
        response.error
        or response.reason
        or f"accepted={response.accepted} rejected={response.rejected} invalid={response.invalid}"
    )


def _collector_response_outcome(status: int, response: CollectorResponse) -> tuple[str, str]:
    retryable, reason = response.retryable_error()
    if retryable:
        return "retryable", reason or _collector_response_summary(response)
    if status in {429, 503}:
        return "retryable", reason or _collector_response_summary(response)
    failed, reason = response.permanent_failure()
    if failed or status >= 300:
        return "permanent", reason or _collector_response_summary(response)
    return "success", ""


def _validate_ingest_envelope(payload: dict) -> None:
    errors: list[str] = []
    if payload.get("api_version") != LOZA_INGEST_API_VERSION:
        errors.append('field "api_version" must match the ingest contract')
    source = payload.get("source")
    if not isinstance(source, dict):
        errors.append('field "source" must be a JSON object')
    else:
        for key in ("sdk", "version", "service"):
            value = source.get(key)
            if not isinstance(value, str) or not value.strip():
                errors.append(f'field "source.{key}" must be a non-empty string')
    events = payload.get("events")
    if not isinstance(events, list) or not events:
        errors.append('field "events" must be a non-empty array')
    elif any(not isinstance(item, dict) for item in events):
        errors.append('field "events" must contain JSON objects only')
    if errors:
        raise ValueError("; ".join(errors))


def _parse_retry_after(value: str | None) -> float | None:
    if not value:
        return None
    value = value.strip()
    if not value:
        return None
    try:
        return float(int(value))
    except ValueError:
        pass
    try:
        parsed = parsedate_to_datetime(value)
    except (TypeError, ValueError, IndexError):
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    delay = (parsed - datetime.now(timezone.utc)).total_seconds()
    return delay if delay > 0 else None


def _retry_delay(attempt: int, base_delay: float, max_delay: float, retry_after: float | None = None) -> float:
    if retry_after is not None:
        return min(max_delay, max(0.0, retry_after))
    return min(max_delay, base_delay * (2**attempt))


class CollectorClient:
    def __init__(
        self,
        endpoint: str,
        api_key: str = "",
        auth_header: str = "Authorization",
        timeout: float = 2.0,
        retries: int = 2,
        sdk_name: str = "loza-py",
        sdk_version: str = SDK_VERSION,
        service: str = "",
        username: str = "",
        password: str = "",
    ) -> None:
        _validate_collector_endpoint(endpoint)
        _validate_basic_auth_endpoint(endpoint, username, password)
        _validate_auth_requires_https(endpoint, api_key=api_key, username=username, password=password)
        self.endpoint = endpoint
        self.api_key = api_key
        self.auth_header = auth_header
        self.timeout = timeout
        self.retries = retries
        self.sdk_name = sdk_name
        self.sdk_version = sdk_version
        self.service = service
        self.username = username
        self.password = password

    def _auth_headers(self) -> dict[str, str]:
        if self.api_key:
            if self.auth_header.lower() == "authorization":
                return {self.auth_header: f"Bearer {self.api_key}"}
            return {self.auth_header: self.api_key}
        if self.username or self.password:
            token = base64.b64encode(f"{self.username}:{self.password}".encode("utf-8")).decode("ascii")
            return {self.auth_header: f"Basic {token}"}
        return {}

    def envelope(self, encoded_events: Iterable[str]) -> bytes:
        return _build_ingest_body(encoded_events, self.sdk_name, self.sdk_version, self.service)

    def send_batch(self, encoded_events: Iterable[str]) -> CollectorResponse:
        body = self.envelope(encoded_events)
        headers = {"content-type": "application/json"}
        headers.update(self._auth_headers())
        request = Request(self.endpoint, data=body, headers=headers, method="POST")
        last_error: Exception | None = None
        for attempt in range(self.retries + 1):
            try:
                try:
                    with _open(request, timeout=self.timeout, endpoint=self.endpoint) as response:
                        status = getattr(response, "status", response.getcode())
                        headers_obj = getattr(response, "headers", None)
                        raw = response.read()
                except HTTPError as exc:
                    status = exc.code
                    headers_obj = exc.headers
                    raw = exc.read()
                payload, parsed = _parse_collector_response_body(raw)
                outcome, reason = _collector_response_outcome(status, parsed)
                if outcome == "success":
                    return parsed
                retry_after = _parse_retry_after(headers_obj.get("Retry-After") if headers_obj else None)
                last_error = RuntimeError(
                    f"collector reported {'retryable batch errors' if outcome == 'retryable' else 'batch failure'}: {reason}"
                )
                if outcome == "retryable" and attempt < self.retries:
                    time.sleep(_retry_delay(attempt, 0.05, 1.0, retry_after))
                    continue
                raise last_error
            except (URLError, TimeoutError, json.JSONDecodeError, ValueError) as exc:
                last_error = exc
                if attempt >= self.retries:
                    break
                time.sleep(min(1.0, 0.05 * (2**attempt)))
        raise RuntimeError(f"collector send failed: {last_error}") from last_error

    def _base_url(self) -> str:
        """Extract host:port base URL from the endpoint."""
        parsed = urlparse(self.endpoint)
        return f"{parsed.scheme}://{parsed.netloc}"

    def health(self) -> bool:
        """Check if the collector is healthy."""
        for path in ("/health", "/healthz"):
            req = Request(f"{self._base_url()}{path}", headers=self._auth_headers(), method="GET")
            try:
                with _open(req, timeout=self.timeout) as resp:
                    data = json.loads(resp.read())
                    if isinstance(data, dict) and data.get("status") == "ok":
                        return True
            except (URLError, HTTPError, OSError, json.JSONDecodeError):
                continue
        return False

    def ready(self) -> bool:
        """Check if the collector is ready to accept requests."""
        for path in ("/ready", "/readyz"):
            req = Request(f"{self._base_url()}{path}", headers=self._auth_headers(), method="GET")
            try:
                with _open(req, timeout=self.timeout) as resp:
                    data = json.loads(resp.read())
                    if isinstance(data, dict) and data.get("status") in ("ok", "ready"):
                        return True
            except (URLError, HTTPError, OSError, json.JSONDecodeError):
                continue
        return False

    def version(self) -> dict:
        """Fetch version info from the collector."""
        req = Request(f"{self._base_url()}/version", headers=self._auth_headers(), method="GET")
        with _open(req, timeout=self.timeout) as resp:
            return json.loads(resp.read())

    def status(self) -> dict:
        """Fetch operational status from the collector."""
        req = Request(f"{self._base_url()}/status", headers=self._auth_headers(), method="GET")
        with _open(req, timeout=self.timeout) as resp:
            return json.loads(resp.read())

    def tail_lines(
        self,
        timeout: float = 30.0,
        service: str = "",
        kind: str = "",
    ):
        """Stream events from the collector's /tail endpoint.

        Yields parsed event dicts as they arrive. Supports optional
        filtering by service and kind via query parameters.

        Args:
            timeout: Socket read timeout in seconds.
            service: Filter events by service name.
            kind: Filter events by kind (event, log, http, etc.).
        """
        url = self.endpoint.rstrip("/") + "/tail"
        params = []
        if service:
            params.append(("service", service))
        if kind:
            params.append(("kind", kind))
        if params:
            url += "?" + urlencode(params)

        request = Request(url, headers={"accept": "application/x-ndjson", **self._auth_headers()})
        with _open(request, timeout=timeout) as response:
            for raw in response:
                line = raw.decode("utf-8").strip()
                if line:
                    try:
                        yield json.loads(line)
                    except json.JSONDecodeError:
                        yield line


def _validate_collector_endpoint(endpoint: str) -> None:
    try:
        parsed = urlparse(endpoint)
        parsed.port
    except ValueError as exc:
        raise ValueError("collector endpoint is malformed") from exc
    if parsed.scheme not in {"http", "https"}:
        raise ValueError("collector endpoint must use http or https")
    if parsed.username is not None or parsed.password is not None:
        raise ValueError("collector endpoint must not contain credentials")
    if not parsed.hostname:
        raise ValueError("collector endpoint must include a host")
    if _private_endpoint_allowed():
        return
    host = parsed.hostname
    if host in {"localhost", "127.0.0.1", "::1"}:
        return
    try:
        addresses = _resolve_collector_addresses(host, parsed.port or _default_port(parsed.scheme))
    except socket.gaierror:
        return
    for raw in addresses:
        if _is_non_public_address(raw):
            raise ValueError("collector endpoint resolves to a non-public address")


_LOCAL_AUTH_HOSTS = frozenset({"localhost", "127.0.0.1", "::1"})


def _validate_basic_auth_endpoint(endpoint: str, username: str, password: str) -> None:
    if not username and password:
        raise ValueError("collector basic auth password requires a username")
    if username and not password and not is_public_dsn_username(username):
        raise ValueError("collector basic auth requires a password unless username is an lz_pub_ capability")
    if not username:
        return
    parsed = urlparse(endpoint)
    if parsed.scheme == "http" and (parsed.hostname or "").lower() not in _LOCAL_AUTH_HOSTS:
        raise ValueError("collector basic auth requires HTTPS except for local endpoints")


def _validate_auth_requires_https(endpoint: str, *, api_key: str = "", username: str = "", password: str = "") -> None:
    """Reject cleartext HTTP for non-local endpoints carrying a credential.

    Basic auth, Bearer tokens, and custom API-key headers must never be sent
    over an unencrypted connection. Loopback hosts stay allowed for local dev.
    """
    if not (api_key or username or password):
        return
    parsed = urlparse(endpoint)
    if parsed.scheme == "http" and (parsed.hostname or "").lower() not in _LOCAL_AUTH_HOSTS:
        raise ValueError("authenticated collector endpoints require HTTPS except for local endpoints")


def _private_endpoint_allowed() -> bool:
    return getenv("LOZA_ALLOW_PRIVATE_COLLECTOR_ENDPOINTS", "").lower() in {"1", "true", "yes"}


def _default_port(scheme: str) -> int:
    return 443 if scheme == "https" else 80


def _origin(url: str) -> tuple[str, str, int]:
    """Return the normalized (scheme, host, port) origin of a URL."""
    try:
        parsed = urlparse(url)
        return (parsed.scheme.lower(), (parsed.hostname or "").lower(), parsed.port or _default_port(parsed.scheme))
    except ValueError as exc:
        raise ValueError("redirect target is malformed") from exc


class _SameOriginRedirectHandler(HTTPRedirectHandler):
    """Refuse redirects that would leave the configured collector origin.

    urllib copies the request's custom headers (including Authorization) onto
    the redirected request, and endpoint validation only happens once at
    construction time. Allowing an arbitrary collector-supplied Location would
    therefore forward credentials to an unvalidated host and enable SSRF.
    """

    def __init__(self, endpoint: str) -> None:
        self._endpoint = endpoint

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        try:
            _validate_collector_endpoint(newurl)
            target_origin = _origin(newurl)
            allowed_origin = _origin(self._endpoint)
        except ValueError:
            # A None result makes urllib surface the 3xx as the final response;
            # callers already treat status >= 300 as a failed send.
            return None
        if target_origin != allowed_origin:
            return None
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def _open_with_redirect_guard(request: Request, timeout: float, endpoint: str):
    """Open a credentialed request without following off-origin redirects."""
    opener = build_opener(_SameOriginRedirectHandler(endpoint))
    return opener.open(request, timeout=timeout)


def _is_non_public_address(raw: str) -> bool:
    try:
        addr = ip_address(raw)
    except ValueError:
        return False
    return (
        addr.is_private
        or addr.is_loopback
        or addr.is_link_local
        or addr.is_multicast
        or addr.is_reserved
    )


def _resolve_collector_addresses(host: str, port: int) -> list[str]:
    return list({info[4][0] for info in socket.getaddrinfo(host, port)})


class _PinnedHTTPConnection(http.client.HTTPConnection):
    """HTTP connection whose socket is pinned to a pre-validated IP address."""

    def __init__(self, *args, pinned_ip: str, **kwargs) -> None:
        super().__init__(*args, **kwargs)
        self._pinned_ip = pinned_ip

    def connect(self) -> None:
        self.sock = socket.create_connection((self._pinned_ip, self.port), self.timeout, self.source_address)
        if self._tunnel_host:
            self._tunnel()


class _PinnedHTTPSConnection(http.client.HTTPSConnection):
    """HTTPS connection pinned to a validated IP but verified against the original host."""

    def __init__(self, *args, pinned_ip: str, **kwargs) -> None:
        super().__init__(*args, **kwargs)
        self._pinned_ip = pinned_ip

    def connect(self) -> None:
        self.sock = socket.create_connection((self._pinned_ip, self.port), self.timeout, self.source_address)
        if self._tunnel_host:
            self._tunnel()
        server_hostname = self.host
        try:
            ip_address((server_hostname or "").strip("[]"))
        except ValueError:
            pass
        else:
            server_hostname = None
        self.sock = self._context.wrap_socket(self.sock, server_hostname=server_hostname)


class _PinnedHTTPHandler(urllib.request.HTTPHandler):
    def __init__(self, pinned_ip: str) -> None:
        super().__init__()
        self._pinned_ip = pinned_ip

    def http_open(self, req):
        return self.do_open(
            lambda host, port=None, **kwargs: _PinnedHTTPConnection(host, port, pinned_ip=self._pinned_ip, **kwargs),
            req,
        )


class _PinnedHTTPSHandler(urllib.request.HTTPSHandler):
    def __init__(self, pinned_ip: str) -> None:
        super().__init__()
        self._pinned_ip = pinned_ip

    def https_open(self, req):
        return self.do_open(
            lambda host, port=None, **kwargs: _PinnedHTTPSConnection(host, port, pinned_ip=self._pinned_ip, **kwargs),
            req,
            context=self._context,
        )


def _pinned_urlopen(request: Request, pinned_ip: str, timeout: float, endpoint: str | None = None):
    handler = (
        _PinnedHTTPSHandler(pinned_ip)
        if urlparse(request.full_url).scheme == "https"
        else _PinnedHTTPHandler(pinned_ip)
    )
    handlers = [handler]
    if endpoint:
        handlers.append(_SameOriginRedirectHandler(endpoint))
    opener = urllib.request.build_opener(*handlers)
    return opener.open(request, timeout=timeout)


def _open(request: Request, timeout: float = 5.0, endpoint: str | None = None):
    """Open a collector request with connection-time SSRF validation.

    The host is resolved and checked immediately before connecting, and the
    socket is pinned to the validated IP so a second DNS answer cannot redirect
    the connection to a private address (DNS rebinding). Hostnames that cannot
    be resolved here fall back to urlopen, preserving offline/mocked callers and
    surfacing any real resolution error through urlopen itself.
    """
    parsed = urlparse(request.full_url)
    host = parsed.hostname
    if not host:
        raise ValueError("collector endpoint must include a host")
    port = parsed.port or _default_port(parsed.scheme)
    allow_private = _private_endpoint_allowed() or host in {"localhost", "127.0.0.1", "::1"}
    try:
        addresses = _resolve_collector_addresses(host, port)
    except socket.gaierror:
        if endpoint:
            return _open_with_redirect_guard(request, timeout, endpoint)
        return urlopen(request, timeout=timeout)
    if not allow_private:
        for raw in addresses:
            if _is_non_public_address(raw):
                raise ValueError("collector endpoint resolves to a non-public address")
    return _pinned_urlopen(request, addresses[0], timeout, endpoint)


def WrapHTTPClient(client=None):
    return client or InstrumentedHTTPClient()


def NewRoundTripper(base=None):
    return base or InstrumentedHTTPClient()


# ── CollectorClient API extensions ──────────────────────────────────────────


def _quote_segment(value: Any) -> str:
    """Percent-encode a single dynamic URL path segment.

    Caller-supplied identifiers must not be able to rewrite the request path
    or inject query parameters/fragments. Encoding each segment with an empty
    ``safe`` set neutralizes ``/``, ``?`` and ``#`` (and other reserved bytes).
    """
    return quote(str(value), safe="")


def _collector_request(
    client: CollectorClient,
    method: str,
    path: str,
    body: Any = None,
    params: dict[str, str] | None = None,
) -> dict:
    from urllib.parse import urlencode

    url = client._base_url().rstrip("/") + "/" + path.lstrip("/")
    if params:
        url += "?" + urlencode(params)
    data = json.dumps(body).encode("utf-8") if body is not None else None
    headers = {"content-type": "application/json"} if body else {}
    headers.update(client._auth_headers())
    req = Request(url, data=data, headers=headers, method=method)
    with _open(req, timeout=client.timeout) as resp:
        raw = resp.read()
        return json.loads(raw.decode("utf-8")) if raw else {}


def _collector_validate(self, payload):
    return _collector_request(self, "POST", "/validate", payload)


def _collector_ingest(self, encoded_events):
    return self.send_batch(encoded_events)


def _collector_query(self, **params):
    return _collector_request(self, "POST", "/query", body=params)


def _collector_tail(self, **params):
    return _collector_request(self, "GET", "/tail", params=params)


def _collector_delete(self, **params):
    event_id = params.get("event_id") or params.get("id")
    if event_id:
        return _collector_request(self, "DELETE", f"/events/{_quote_segment(event_id)}")
    tenant_id = params.get("tenant_id")
    if tenant_id:
        return _collector_request(self, "DELETE", f"/events/by-tenant/{_quote_segment(tenant_id)}")
    user_id = params.get("user_id")
    if user_id:
        return _collector_request(self, "DELETE", f"/events/by-user/{_quote_segment(user_id)}")
    raise ValueError("delete requires one of: event_id/id, tenant_id, user_id")


def _collector_replay(self, **params):
    return _collector_request(self, "POST", "/replay", body=params)


def _collector_dlq_list(self, **params):
    return _collector_request(self, "GET", "/dlq", params=params)


def _collector_dlq_read(self, dlq_id):
    return _collector_request(self, "GET", f"/dlq/{_quote_segment(dlq_id)}")


def _collector_dlq_replay(self, dlq_id):
    return _collector_request(self, "POST", f"/dlq/{_quote_segment(dlq_id)}/replay")


def _collector_keys_create(self, **params):
    return _collector_request(self, "POST", "/keys", body=params)


def _collector_keys_revoke(self, key_id):
    return _collector_request(self, "DELETE", f"/keys/{_quote_segment(key_id)}")


def _collector_keys_rotate(self, key_id):
    return _collector_request(self, "POST", f"/keys/{_quote_segment(key_id)}/rotate")


def _collector_sinks_list(self):
    return _collector_request(self, "GET", "/sinks")


def _collector_sinks_test(self, name):
    return _collector_request(self, "POST", f"/sinks/{_quote_segment(name)}/test")


def _collector_policy_validate(self, policy):
    return _collector_request(self, "POST", "/policy/validate", policy)


def _collector_schema_check(self, event):
    return _collector_request(self, "POST", "/schema/check", event)


def _collector_schema_publish(self, schema):
    return _collector_request(self, "POST", "/schema/publish", schema)


def _collector_retention_apply(self, policy=None):
    return _collector_request(self, "POST", "/retention/apply", body=policy or {})


CollectorClient.validate = _collector_validate
CollectorClient.ingest = _collector_ingest
CollectorClient.query = _collector_query
CollectorClient.tail = _collector_tail
CollectorClient.delete = _collector_delete
CollectorClient.replay = _collector_replay
CollectorClient.dlq_list = _collector_dlq_list
CollectorClient.dlq_read = _collector_dlq_read
CollectorClient.dlq_replay = _collector_dlq_replay
CollectorClient.keys_create = _collector_keys_create
CollectorClient.keys_revoke = _collector_keys_revoke
CollectorClient.keys_rotate = _collector_keys_rotate
CollectorClient.sinks_list = _collector_sinks_list
CollectorClient.sinks_test = _collector_sinks_test
CollectorClient.policy_validate = _collector_policy_validate
CollectorClient.schema_check = _collector_schema_check
CollectorClient.schema_publish = _collector_schema_publish
CollectorClient.retention_apply = _collector_retention_apply
