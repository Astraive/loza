use base64::Engine as _;
use serde::{Deserialize, Serialize};
use serde_json::Value;

use std::collections::BTreeMap;
use std::io;
use std::time::Duration;

use crate::generated::spec_contract::{
    build_ingest_envelope, parse_collector_response_value,
    CollectorResponse as SpecCollectorResponse,
};

#[derive(Clone, Debug)]
pub struct HTTPRequest {
    pub method: String,
    pub url: String,
    pub headers: BTreeMap<String, String>,
    pub body: Option<Vec<u8>>,
}

impl HTTPRequest {
    pub fn new(method: impl Into<String>, url: impl Into<String>) -> Self {
        Self {
            method: method.into(),
            url: url.into(),
            headers: BTreeMap::new(),
            body: None,
        }
    }

    pub fn with_header(mut self, key: impl Into<String>, value: impl Into<String>) -> Self {
        self.headers.insert(key.into(), value.into());
        self
    }

    pub fn with_body(mut self, body: impl Into<Vec<u8>>) -> Self {
        self.body = Some(body.into());
        self
    }
}

#[derive(Clone, Debug)]
pub struct HTTPResponse {
    pub status_code: u16,
    pub headers: BTreeMap<String, String>,
    pub body: String,
}

#[derive(Clone, Debug)]
pub struct HTTPClient {
    agent: ureq::Agent,
}

impl Default for HTTPClient {
    fn default() -> Self {
        Self::new()
    }
}

impl HTTPClient {
    pub fn new() -> Self {
        Self::with_timeout_ms(2_000)
    }

    pub fn with_timeout_ms(timeout_ms: u64) -> Self {
        Self::with_timeout_ms_and_insecure(timeout_ms, false)
    }

    /// Build a transport that optionally skips TLS certificate verification.
    ///
    /// `insecure` is only intended for local development against collectors
    /// that present self-signed certificates; verified TLS remains the default.
    pub(crate) fn with_timeout_ms_and_insecure(timeout_ms: u64, insecure: bool) -> Self {
        let mut builder = ureq::AgentBuilder::new().timeout(Duration::from_millis(timeout_ms));
        if insecure {
            builder = builder.tls_config(insecure_tls_config());
        }
        Self {
            agent: builder.build(),
        }
    }

    pub fn send(&self, request: &HTTPRequest) -> io::Result<HTTPResponse> {
        self.send_raw(request)
    }

    pub fn send_with_context(
        &self,
        ctx: &mut crate::EventContext,
        request: &HTTPRequest,
    ) -> io::Result<HTTPResponse> {
        let mut request = request.clone();
        inject_http_headers(&mut request, ctx);

        // Add "http.client.started" checkpoint
        let mut started = serde_json::Map::new();
        started.insert(
            "name".to_string(),
            serde_json::Value::String("http.client.started".to_string()),
        );
        started.insert(
            "http.client.method".to_string(),
            serde_json::Value::String(request.method.clone()),
        );
        ctx.checkpoints.push(started);

        let response = self.send_raw(&request)?;

        // Add "http.client.finished" checkpoint
        let mut finished = serde_json::Map::new();
        finished.insert(
            "name".to_string(),
            serde_json::Value::String("http.client.finished".to_string()),
        );
        finished.insert(
            "http.client.status_code".to_string(),
            serde_json::Value::Number(response.status_code.into()),
        );
        ctx.checkpoints.push(finished);

        Ok(response)
    }

    fn send_raw(&self, request: &HTTPRequest) -> io::Result<HTTPResponse> {
        let mut req = match request.method.to_uppercase().as_str() {
            "GET" => self.agent.get(&request.url),
            "POST" => self.agent.post(&request.url),
            "PUT" => self.agent.put(&request.url),
            "DELETE" => self.agent.delete(&request.url),
            other => {
                return Err(io::Error::new(
                    io::ErrorKind::InvalidInput,
                    format!("unsupported method: {other}"),
                ))
            }
        };
        for (key, value) in &request.headers {
            req = req.set(key, value);
        }
        let response = if let Some(body) = &request.body {
            req.send_bytes(body)
        } else {
            req.call()
        };
        // ureq 2.x reports HTTP status codes >= 400 as `Err(Error::Status(_, response))`.
        // The response still carries the status, headers and body, so return it as a
        // successful `HTTPResponse` rather than collapsing it into a transport error.
        let response = match response {
            Ok(response) => response,
            Err(ureq::Error::Status(_, response)) => response,
            Err(err) => return Err(io::Error::other(err)),
        };
        let status_code = response.status();
        let mut headers = BTreeMap::new();
        for name in response.headers_names() {
            if let Some(value) = response.header(&name) {
                headers.insert(name, value.to_string());
            }
        }
        let body = response.into_string().map_err(io::Error::other)?;
        Ok(HTTPResponse {
            status_code,
            headers,
            body,
        })
    }
}

/// Certificate verifier used only when `insecure` is enabled.
///
/// It deliberately accepts any server certificate so a client can reach a
/// collector that presents a self-signed certificate during local development.
#[derive(Debug)]
struct AcceptAnyServerCert;

impl ureq::rustls::client::danger::ServerCertVerifier for AcceptAnyServerCert {
    fn verify_server_cert(
        &self,
        _end_entity: &ureq::rustls::pki_types::CertificateDer<'_>,
        _intermediates: &[ureq::rustls::pki_types::CertificateDer<'_>],
        _server_name: &ureq::rustls::pki_types::ServerName<'_>,
        _ocsp_response: &[u8],
        _now: ureq::rustls::pki_types::UnixTime,
    ) -> Result<ureq::rustls::client::danger::ServerCertVerified, ureq::rustls::Error> {
        Ok(ureq::rustls::client::danger::ServerCertVerified::assertion())
    }

    fn verify_tls12_signature(
        &self,
        _message: &[u8],
        _cert: &ureq::rustls::pki_types::CertificateDer<'_>,
        _dss: &ureq::rustls::DigitallySignedStruct,
    ) -> Result<ureq::rustls::client::danger::HandshakeSignatureValid, ureq::rustls::Error> {
        Ok(ureq::rustls::client::danger::HandshakeSignatureValid::assertion())
    }

    fn verify_tls13_signature(
        &self,
        _message: &[u8],
        _cert: &ureq::rustls::pki_types::CertificateDer<'_>,
        _dss: &ureq::rustls::DigitallySignedStruct,
    ) -> Result<ureq::rustls::client::danger::HandshakeSignatureValid, ureq::rustls::Error> {
        Ok(ureq::rustls::client::danger::HandshakeSignatureValid::assertion())
    }

    fn supported_verify_schemes(&self) -> Vec<ureq::rustls::SignatureScheme> {
        ureq::rustls::crypto::ring::default_provider()
            .signature_verification_algorithms
            .supported_schemes()
    }
}

/// Build a rustls client config that skips certificate verification.
fn insecure_tls_config() -> std::sync::Arc<ureq::rustls::ClientConfig> {
    use ureq::rustls;

    let config = rustls::ClientConfig::builder_with_provider(
        rustls::crypto::ring::default_provider().into(),
    )
    .with_protocol_versions(&[&rustls::version::TLS12, &rustls::version::TLS13])
    .expect("ring supports TLS 1.2 and TLS 1.3")
    .dangerous()
    .with_custom_certificate_verifier(std::sync::Arc::new(AcceptAnyServerCert))
    .with_no_client_auth();
    std::sync::Arc::new(config)
}

/// Collector-specific HTTP client with envelope support.
#[derive(Clone)]
pub struct CollectorHttpClient {
    pub endpoint: String,
    pub collector_name: Option<String>,
    pub environment: Option<String>,
    pub api_key: Option<String>,
    pub basic_username: Option<String>,
    pub basic_password: Option<String>,
    pub insecure: bool,
    pub auth_header: String,
    pub timeout_ms: u64,
    pub sdk_name: String,
    pub sdk_version: String,
    pub service: Option<String>,
}
impl std::fmt::Debug for CollectorHttpClient {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("CollectorHttpClient")
            .field("endpoint", &self.endpoint)
            .field("api_key", &self.api_key.as_ref().map(|_| "<redacted>"))
            .field(
                "basic_credentials",
                &self.basic_username.as_ref().map(|_| "<redacted>"),
            )
            .field("insecure", &self.insecure)
            .field("auth_header", &self.auth_header)
            .field("timeout_ms", &self.timeout_ms)
            .field("sdk_name", &self.sdk_name)
            .field("sdk_version", &self.sdk_version)
            .field("service", &self.service)
            .finish()
    }
}

impl CollectorHttpClient {
    pub fn new(endpoint: impl Into<String>) -> Self {
        let endpoint = normalize_collector_endpoint(endpoint.into());
        Self {
            endpoint,
            collector_name: None,
            environment: None,
            api_key: None,
            basic_username: None,
            basic_password: None,
            insecure: false,
            auth_header: "Authorization".to_string(),
            timeout_ms: 2_000,
            sdk_name: "loza-rs".to_string(),
            sdk_version: crate::core::version::sdk_version().to_string(),
            service: None,
        }
    }

    pub fn with_api_key(mut self, api_key: impl Into<String>) -> Self {
        self.api_key = Some(api_key.into());
        self
    }

    pub fn with_collector(mut self, collector: impl Into<String>) -> Self {
        self.collector_name = Some(collector.into());
        self
    }

    pub fn with_environment(mut self, environment: impl Into<String>) -> Self {
        self.environment = Some(environment.into());
        self
    }

    pub fn with_basic_auth(
        mut self,
        username: impl Into<String>,
        password: impl Into<String>,
    ) -> Self {
        self.basic_username = Some(username.into());
        self.basic_password = Some(password.into());
        self
    }

    /// Allow insecure transport: plaintext HTTP for credentialed endpoints and
    /// TLS connections without server-certificate verification (self-signed
    /// collectors). Disabled by default; enable only for local development.
    pub fn with_insecure(mut self, insecure: bool) -> Self {
        self.insecure = insecure;
        self
    }

    pub fn with_timeout_ms(mut self, timeout_ms: u64) -> Self {
        self.timeout_ms = timeout_ms;
        self
    }

    pub fn with_service(mut self, service: impl Into<String>) -> Self {
        self.service = Some(service.into());
        self
    }

    /// Build an ingest envelope from encoded events.
    ///
    /// Events that cannot be parsed as JSON are preserved as
    /// `{"malformed": <original encoded event>}` so they are never silently
    /// dropped. Use [`Self::try_envelope`] when malformed events must instead be
    /// reported to the caller as an error.
    pub fn envelope(&self, encoded_events: &[String]) -> Value {
        let events: Vec<Value> = encoded_events
            .iter()
            .map(|event| {
                serde_json::from_str(event)
                    .unwrap_or_else(|_| serde_json::json!({ "malformed": event }))
            })
            .collect();
        self.build_envelope(&events)
    }

    /// Build an ingest envelope from encoded events, rejecting malformed input.
    ///
    /// Returns an error naming the index and parse failure of the first event
    /// that is not valid JSON. Unlike [`Self::envelope`], no event is dropped or
    /// replaced and the caller learns which input is invalid.
    pub fn try_envelope(&self, encoded_events: &[String]) -> Result<Value, String> {
        let events = encoded_events
            .iter()
            .enumerate()
            .map(|(index, event)| {
                serde_json::from_str(event)
                    .map_err(|error| format!("event at index {index} is not valid JSON: {error}"))
            })
            .collect::<Result<Vec<Value>, String>>()?;
        Ok(self.build_envelope(&events))
    }

    fn build_envelope(&self, events: &[Value]) -> Value {
        let service = self
            .service
            .clone()
            .or_else(|| infer_service(events))
            .unwrap_or_else(|| "unknown".to_string());
        build_ingest_envelope(events, &self.sdk_name, &self.sdk_version, &service)
    }

    /// Validate an ingest envelope.
    pub fn validate_envelope(&self, envelope: &Value) -> Result<(), String> {
        validate_ingest_envelope(envelope)
    }

    /// Parse a collector response.
    pub fn parse_response(&self, body: &str) -> Result<SpecCollectorResponse, serde_json::Error> {
        let value: Value = serde_json::from_str(body)?;
        parse_collector_response_value(&value)
    }

    /// Get the tail endpoint URL.
    pub fn tail_endpoint(&self) -> String {
        format!("{}/tail", self.endpoint.trim_end_matches('/'))
    }

    /// Send an authenticated HTTP request to the collector.
    fn request(
        &self,
        method: &str,
        path: &str,
        body: Option<Value>,
    ) -> Result<CollectorResponse, String> {
        let url = format!("{}{}", self.endpoint.trim_end_matches('/'), path);
        let client = HTTPClient::with_timeout_ms_and_insecure(self.timeout_ms, self.insecure);
        let mut request = HTTPRequest::new(method, &url);

        // Any configured credential (Bearer API key or Basic auth) must never be
        // transmitted to a remote collector over plaintext HTTP. Validate the
        // endpoint before either auth branch adds the credential to the request.
        let has_api_key = self.api_key.is_some();
        let has_basic_auth = self.basic_username.is_some() && self.basic_password.is_some();
        if has_api_key || has_basic_auth {
            validate_credentialed_endpoint(&self.endpoint, self.insecure)?;
        }

        if let Some(api_key) = &self.api_key {
            request = request.with_header(&self.auth_header, format!("Bearer {}", api_key));
        } else {
            match (&self.basic_username, &self.basic_password) {
                (Some(username), Some(password)) => {
                    validate_basic_auth_username(username)?;
                    let token = base64::engine::general_purpose::STANDARD
                        .encode(format!("{username}:{password}"));
                    request = request.with_header(&self.auth_header, format!("Basic {token}"));
                }
                (None, None) => {}
                _ => {
                    return Err("basic username and password must be configured together".into());
                }
            }
        }

        request = request
            .with_header(
                "User-Agent",
                format!("{}/{}", self.sdk_name, self.sdk_version),
            )
            .with_header("Content-Type", "application/json");
        if let Some(environment) = &self.environment {
            request = request.with_header("X-Loza-Env", environment);
        }
        if let Some(service) = &self.service {
            request = request.with_header("X-Loza-Service", service);
        }

        if let Some(body) = body {
            let bytes = serde_json::to_vec(&body).map_err(|e| e.to_string())?;
            request = request.with_body(bytes);
        }

        let response = client.send(&request).map_err(|e| e.to_string())?;
        let body: Value = serde_json::from_str(&response.body).map_err(|e| e.to_string())?;
        Ok(CollectorResponse {
            status_code: response.status_code,
            body,
        })
    }

    /// Validate events locally against the ingest envelope contract.
    /// The collector does not expose a dedicated `/validate` endpoint.
    pub fn validate(&self, events: &[String]) -> Result<CollectorResponse, String> {
        let envelope = self.try_envelope(events)?;
        self.validate_envelope(&envelope)?;
        Ok(CollectorResponse {
            status_code: 200,
            body: serde_json::json!({
                "status": "accepted",
                "valid": true
            }),
        })
    }

    /// Ingest events into the collector.
    pub fn ingest(&self, events: &[String]) -> Result<CollectorResponse, String> {
        let envelope = self.try_envelope(events)?;
        self.request("POST", "/events", Some(envelope))
    }

    /// Query events from the collector.
    pub fn query(&self, query: &str) -> Result<CollectorResponse, String> {
        self.request("POST", "/query", Some(serde_json::json!({"query": query})))
    }

    /// Tail recent events from the collector.
    pub fn tail(&self, count: u32) -> Result<CollectorResponse, String> {
        self.request("GET", &format!("/tail?limit={}", count), None)
    }

    /// Delete events from the collector by event ID.
    pub fn delete(&self, event_id: &str) -> Result<CollectorResponse, String> {
        let segment = encode_path_segment(event_id)?;
        self.request("DELETE", &format!("/events/{segment}"), None)
    }

    /// Replay events from the dead-letter queue.
    pub fn replay(&self, event_ids: &[String]) -> Result<CollectorResponse, String> {
        self.request(
            "POST",
            "/replay",
            Some(serde_json::json!({"event_ids": event_ids})),
        )
    }

    /// List dead-letter queue entries.
    pub fn dlq_list(&self, limit: u32) -> Result<CollectorResponse, String> {
        self.request("GET", &format!("/dlq?limit={}", limit), None)
    }

    /// Read a dead-letter queue entry.
    pub fn dlq_read(&self, entry_id: &str) -> Result<CollectorResponse, String> {
        let segment = encode_path_segment(entry_id)?;
        self.request("GET", &format!("/dlq/{segment}"), None)
    }

    /// Replay specific dead-letter queue entries.
    pub fn dlq_replay(&self, entry_ids: &[String]) -> Result<CollectorResponse, String> {
        self.request(
            "POST",
            "/dlq/replay",
            Some(serde_json::json!({"entry_ids": entry_ids})),
        )
    }

    /// Create an API key.
    pub fn keys_create(&self, name: &str) -> Result<CollectorResponse, String> {
        self.request("POST", "/keys", Some(serde_json::json!({"name": name})))
    }

    /// Revoke an API key.
    pub fn keys_revoke(&self, key_id: &str) -> Result<CollectorResponse, String> {
        let segment = encode_path_segment(key_id)?;
        self.request("DELETE", &format!("/keys/{segment}"), None)
    }

    /// List configured sinks on the collector.
    pub fn sinks_list(&self) -> Result<CollectorResponse, String> {
        self.request("GET", "/sinks", None)
    }

    /// Test a configured sink on the collector.
    pub fn sinks_test(&self, name: &str) -> Result<CollectorResponse, String> {
        let segment = encode_path_segment(name)?;
        self.request("POST", &format!("/sinks/{segment}/test"), None)
    }

    /// Rotate an API key.
    pub fn keys_rotate(&self, key_id: &str) -> Result<CollectorResponse, String> {
        let segment = encode_path_segment(key_id)?;
        self.request("POST", &format!("/keys/{segment}/rotate"), None)
    }

    /// Validate an event governance policy.
    pub fn policy_validate(&self, policy: &Value) -> Result<CollectorResponse, String> {
        self.request("POST", "/policy/validate", Some(policy.clone()))
    }

    /// Check an event against the active schema.
    pub fn schema_check(&self, event: &Value) -> Result<CollectorResponse, String> {
        self.request("POST", "/schema/check", Some(event.clone()))
    }

    /// Publish schema metadata.
    pub fn schema_publish(&self, schema: &Value) -> Result<CollectorResponse, String> {
        self.request("POST", "/schema/publish", Some(schema.clone()))
    }

    /// Apply retention policy immediately.
    pub fn retention_apply(&self, policy: &Value) -> Result<CollectorResponse, String> {
        self.request("POST", "/retention/apply", Some(policy.clone()))
    }

    /// Check collector health.
    pub fn health(&self) -> Result<CollectorResponse, String> {
        self.request("GET", "/health", None)
    }
    pub fn query_lql(
        &self,
        source: &str,
        parameters: std::collections::HashMap<String, QueryValue>,
        limit: usize,
    ) -> Result<QueryResult, QueryLqlError> {
        if source.trim().is_empty() {
            return Err(QueryLqlError::new(
                "invalid_configuration",
                "LQL query source is required",
                None,
            ));
        }
        let route = match &self.collector_name {
            Some(collector) => {
                let segment = encode_path_segment(collector)
                    .map_err(|error| QueryLqlError::new("invalid_configuration", &error, None))?;
                format!("/collectors/{segment}/lql/query")
            }
            None => "/lql/query".to_string(),
        };
        let response = self
            .request(
                "POST",
                &route,
                Some(serde_json::json!({
                    "query": source,
                    "parameters": parameters,
                    "limit": limit.clamp(1, 1000)
                })),
            )
            .map_err(|error| {
                QueryLqlError::new("transport", "LQL query transport failed", Some(error))
            })?;
        if response.status_code >= 300 {
            let category = match response.status_code {
                400 => "diagnostics",
                401 => "authentication",
                403 => "scope",
                503 => "compiler_unavailable",
                _ => "execution",
            };
            return Err(QueryLqlError::new(
                category,
                "LQL query failed",
                Some(response.status_code.to_string()),
            ));
        }
        let columns = response
            .body
            .get("columns")
            .and_then(Value::as_array)
            .ok_or_else(|| {
                QueryLqlError::new(
                    "malformed_response",
                    "LQL response has invalid columns",
                    None,
                )
            })?
            .iter()
            .map(|column| {
                if let Some(name) = column.as_str() {
                    Ok(QueryColumn {
                        name: name.into(),
                        value_type: String::new(),
                    })
                } else {
                    serde_json::from_value(column.clone()).map_err(|_| {
                        QueryLqlError::new(
                            "malformed_response",
                            "LQL response has invalid columns",
                            None,
                        )
                    })
                }
            })
            .collect::<Result<Vec<_>, _>>()?;
        let rows =
            serde_json::from_value(response.body.get("rows").cloned().unwrap_or(Value::Null))
                .map_err(|_| {
                    QueryLqlError::new("malformed_response", "LQL response has invalid rows", None)
                })?;
        let row_count = response
            .body
            .get("row_count")
            .and_then(Value::as_u64)
            .unwrap_or(0) as usize;
        let duration_ms = response
            .body
            .get("duration_ms")
            .and_then(Value::as_i64)
            .unwrap_or(0);
        Ok(QueryResult {
            columns,
            rows,
            duration_ms,
            row_count,
        })
    }
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct QueryValue {
    #[serde(rename = "type")]
    pub value_type: String,
    pub value: Value,
}

impl QueryValue {
    pub fn new(value_type: impl Into<String>, value: impl Serialize) -> Self {
        Self {
            value_type: value_type.into(),
            value: serde_json::to_value(value).unwrap_or(Value::Null),
        }
    }
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct QueryColumn {
    pub name: String,
    #[serde(default, rename = "type")]
    pub value_type: String,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct QueryResult {
    pub columns: Vec<QueryColumn>,
    pub rows: Vec<std::collections::HashMap<String, Value>>,
    #[serde(default)]
    pub duration_ms: i64,
    #[serde(default)]
    pub row_count: usize,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct QueryLqlError {
    pub category: String,
    pub message: String,
    pub status: Option<String>,
}

impl QueryLqlError {
    fn new(category: &str, message: &str, status: Option<String>) -> Self {
        Self {
            category: category.into(),
            message: message.into(),
            status,
        }
    }
}

impl std::fmt::Display for QueryLqlError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.message)
    }
}

impl std::error::Error for QueryLqlError {}

/// Collector response wrapper.
#[derive(Clone, Debug)]
pub struct CollectorResponse {
    pub status_code: u16,
    pub body: Value,
}

/// Inject trace context into HTTP headers for propagation.
pub fn inject_http_headers(request: &mut HTTPRequest, event: &crate::EventContext) {
    if let (Some(trace_id), Some(span_id)) = (&event.trace_id, &event.span_id) {
        if trace_id.len() == 32 && span_id.len() == 16 {
            let traceparent = format!(
                "00-{}-{}-01",
                trace_id.to_lowercase(),
                span_id.to_lowercase()
            );
            request
                .headers
                .insert("traceparent".to_string(), traceparent);
        }
    }
    if let Some(trace_id) = &event.trace_id {
        request
            .headers
            .insert("x-trace-id".to_string(), trace_id.clone());
    }
    if let Some(span_id) = &event.span_id {
        request
            .headers
            .insert("x-span-id".to_string(), span_id.clone());
    }
    request
        .headers
        .insert("x-request-id".to_string(), event.request_id.clone());
}

/// Reject endpoint configurations that would expose credentials in cleartext.
///
/// Applies to every authenticated request, whether the credential is a Bearer
/// API key or HTTP Basic auth. HTTPS is always allowed; plaintext HTTP is only
/// permitted for explicit loopback endpoints or when `insecure` is set.
fn validate_credentialed_endpoint(endpoint: &str, insecure: bool) -> Result<(), String> {
    let Some((scheme, rest)) = endpoint.split_once("://") else {
        return Ok(());
    };
    let authority = rest.split(['/', '?', '#']).next().unwrap_or_default();
    if authority.contains('@') {
        return Err("collector credentials must not be embedded in the endpoint URL".into());
    }
    if !scheme.eq_ignore_ascii_case("http") || insecure {
        return Ok(());
    }
    let host = if let Some(ipv6) = authority.strip_prefix('[') {
        ipv6.split(']').next().unwrap_or(ipv6)
    } else {
        authority.split(':').next().unwrap_or(authority)
    };
    if matches!(host, "localhost" | "127.0.0.1" | "::1") {
        return Ok(());
    }
    Err("credentialed HTTP requires TLS (set insecure explicitly for local development)".into())
}

fn validate_basic_auth_username(username: &str) -> Result<(), String> {
    if username.is_empty() || username.contains(':') || username.chars().any(char::is_whitespace) {
        return Err(
            "basic auth username must be non-empty and contain no ':' or whitespace".into(),
        );
    }
    Ok(())
}

pub fn extract_http_headers(
    headers: &BTreeMap<String, String>,
) -> (Option<String>, Option<String>, Option<String>) {
    let trace_id = headers.get("x-trace-id").cloned();
    let span_id = headers.get("x-span-id").cloned();
    let request_id = headers.get("x-request-id").cloned();
    (trace_id, span_id, request_id)
}

fn infer_service(events: &[Value]) -> Option<String> {
    for event in events {
        if let Some(service) = event.get("service").and_then(Value::as_str) {
            if !service.is_empty() {
                return Some(service.to_string());
            }
        }
    }
    None
}

/// Percent-encode a single URL path segment.
///
/// Untrusted identifiers are interpolated into request paths below. Without
/// encoding, a value such as `../keys`, `?x=1` or `#frag` would change the
/// request target, allowing path/query injection on the collector. Every byte
/// outside the RFC 3986 unreserved set is escaped.
///
/// A bare `.` or `..` is rejected: URL parsers normalize dot segments (even
/// percent-encoded ones such as `%2E%2E`) away, so these cannot be represented
/// safely as a single path segment.
fn encode_path_segment(value: &str) -> Result<String, String> {
    if matches!(value, "." | "..") {
        return Err(format!(
            "invalid path segment {value:?}: dot segments are not allowed"
        ));
    }
    const HEX: &[u8; 16] = b"0123456789ABCDEF";
    let mut encoded = String::with_capacity(value.len());
    for &byte in value.as_bytes() {
        if byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'.' | b'_' | b'~') {
            encoded.push(byte as char);
        } else {
            encoded.push('%');
            encoded.push(HEX[(byte >> 4) as usize] as char);
            encoded.push(HEX[(byte & 0x0F) as usize] as char);
        }
    }
    Ok(encoded)
}

fn normalize_collector_endpoint(endpoint: String) -> String {
    let mut base = endpoint.trim().trim_end_matches('/').to_string();
    for suffix in ["/events", "/events/batch"] {
        if base.ends_with(suffix) {
            base.truncate(base.len() - suffix.len());
            break;
        }
    }
    base.trim_end_matches('/').to_string()
}

/// Validate an ingest envelope against the spec.
pub fn validate_ingest_envelope(envelope: &Value) -> Result<(), String> {
    let object = envelope
        .as_object()
        .ok_or_else(|| "collector envelope must be a JSON object".to_string())?;

    let api_version = object
        .get("api_version")
        .and_then(Value::as_str)
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .ok_or_else(|| "collector envelope must include api_version".to_string())?;
    if api_version != crate::generated::spec_contract::LOZA_INGEST_API_VERSION {
        return Err(format!(
            "collector envelope api_version must be {}",
            crate::generated::spec_contract::LOZA_INGEST_API_VERSION
        ));
    }

    let source = object
        .get("source")
        .and_then(Value::as_object)
        .ok_or_else(|| "collector envelope must include a source object".to_string())?;
    for key in ["sdk", "version", "service"] {
        match source.get(key).and_then(Value::as_str).map(str::trim) {
            Some(value) if !value.is_empty() => {}
            _ => {
                return Err(format!(
                    "collector envelope source.{key} must be a non-empty string"
                ))
            }
        }
    }

    let events = object
        .get("events")
        .and_then(Value::as_array)
        .ok_or_else(|| "collector envelope must include an events array".to_string())?;
    if events.is_empty() {
        return Err("collector envelope must include at least one event".to_string());
    }
    for (idx, event) in events.iter().enumerate() {
        if event.as_object().is_none() {
            return Err(format!(
                "collector envelope events[{idx}] must be JSON objects"
            ));
        }
    }

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::net::TcpListener;
    use std::sync::Arc;
    use std::thread;

    // Throwaway, test-only self-signed Ed25519 certificate for 127.0.0.1,
    // valid until 2126. The embedded key protects no real data; it exists only
    // so tests can stand up a local endpoint with an untrusted certificate.
    const TEST_CERT_DER_B64: &str = "MIIBWzCCAQ2gAwIBAgIUWmV7cGsyqWPXSGO3Ee+3H86S/aMwBQYDK2VwMBQxEjAQBgNVBAMMCWxvY2FsaG9zdDAgFw0yNjExMTkwMjU5NTVaGA8yMTI2MTAyNjAyNTk1NVowFDESMBAGA1UEAwwJbG9jYWxob3N0MCowBQYDK2VwAyEAHCJt94grB1Z0Eq+ju2GSEHIBOwSuDz0g8oEC6fvdoGmjbzBtMB0GA1UdDgQWBBS9tlrvG7d3klZTzulCzK6lhtjiwzAfBgNVHSMEGDAWgBS9tlrvG7d3klZTzulCzK6lhtjiwzAPBgNVHRMBAf8EBTADAQH/MBoGA1UdEQQTMBGHBH8AAAGCCWxvY2FsaG9zdDAFBgMrZXADQQDBu0ClvN1iaSyLKgV3s6EQ9kGDj6s0BKxweANfswKjWLMk07mjLTIe101N1c9m6xH+dOc5WL1JVOdCnz+I3WYH";
    const TEST_KEY_DER_B64: &str =
        "MC4CAQAwBQYDK2VwBCIEIDC2pB2KrObVfnggexU0ZWEIr+wDZbdpt5GNprfa5uL6";

    fn accept_tls(
        config: &Arc<ureq::rustls::ServerConfig>,
        mut stream: std::net::TcpStream,
    ) -> Option<ureq::rustls::StreamOwned<ureq::rustls::ServerConnection, std::net::TcpStream>>
    {
        use ureq::rustls;

        let mut acceptor = rustls::server::Acceptor::default();
        let accepted = loop {
            acceptor.read_tls(&mut stream).ok()?;
            if let Some(accepted) = acceptor.accept().ok()? {
                break accepted;
            }
        };
        let conn = accepted.into_connection(config.clone()).ok()?;
        Some(rustls::StreamOwned::new(conn, stream))
    }

    fn spawn_tls_collector() -> (u16, thread::JoinHandle<()>) {
        use ureq::rustls;

        let cert = rustls::pki_types::CertificateDer::from(
            base64::engine::general_purpose::STANDARD
                .decode(TEST_CERT_DER_B64)
                .expect("valid test certificate"),
        );
        let key = rustls::pki_types::PrivatePkcs8KeyDer::from(
            base64::engine::general_purpose::STANDARD
                .decode(TEST_KEY_DER_B64)
                .expect("valid test key"),
        );
        let server_config = rustls::ServerConfig::builder_with_provider(
            rustls::crypto::ring::default_provider().into(),
        )
        .with_protocol_versions(&[&rustls::version::TLS12, &rustls::version::TLS13])
        .expect("ring supports TLS 1.2 and TLS 1.3")
        .with_no_client_auth()
        .with_single_cert(vec![cert], key.into())
        .expect("valid test certificate/key pair");
        let server_config = Arc::new(server_config);

        let listener = TcpListener::bind("127.0.0.1:0").expect("bind loopback");
        let port = listener.local_addr().expect("local address").port();
        let handle = thread::spawn(move || {
            // First connection: verifying client (handshake must fail).
            // Second connection: insecure client (handshake must succeed).
            for _ in 0..2 {
                let Ok((stream, _)) = listener.accept() else {
                    break;
                };
                let Some(mut tls) = accept_tls(&server_config, stream) else {
                    continue;
                };
                let mut buf = [0_u8; 4096];
                let _ = tls.read(&mut buf);
                let body = r#"{"status":"accepted"}"#;
                let response = format!(
                    "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
                    body.len(),
                    body
                );
                let _ = tls.write_all(response.as_bytes());
                let _ = tls.flush();
            }
        });
        (port, handle)
    }

    #[test]
    fn insecure_flag_disables_tls_verification_for_collector_requests() {
        let (port, server) = spawn_tls_collector();
        let endpoint = format!("https://127.0.0.1:{port}");

        // Default behaviour: server certificate verification is enforced.
        let verifying = CollectorHttpClient::new(&endpoint).with_api_key("token");
        assert!(
            verifying.query("from events").is_err(),
            "verifying client must reject the self-signed certificate"
        );

        // Regression: with_insecure(true) must actually reach the collector.
        let insecure = CollectorHttpClient::new(&endpoint)
            .with_api_key("token")
            .with_insecure(true);
        let result = insecure.query("from events");
        assert!(
            result.is_ok(),
            "insecure client should reach a self-signed collector: {result:?}"
        );
        assert_eq!(result.expect("successful response").status_code, 200);
        server.join().expect("collector thread");
    }
}
