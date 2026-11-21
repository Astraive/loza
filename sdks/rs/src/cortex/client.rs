use std::io;
use std::time::Duration;

use url::Url;

use super::schema::{GraphView, IncidentContext, Remediation, RemediationFeedback};

/// Client for the Cortex incident intelligence API.
///
/// Provides methods for incident reconstruction, graph queries,
/// similar incident lookup, and remediation recording.
pub struct CortexClient {
    endpoint: String,
    api_key: Option<String>,
    auth_header: String,
    timeout: Duration,
}

impl CortexClient {
    pub fn new(endpoint: impl Into<String>) -> Self {
        Self {
            endpoint: endpoint.into().trim_end_matches('/').to_string(),
            api_key: None,
            auth_header: "x-loza-api-key".to_string(),
            timeout: Duration::from_secs(10),
        }
    }

    pub fn with_api_key(mut self, api_key: impl Into<String>) -> Self {
        self.api_key = Some(api_key.into());
        self
    }

    pub fn with_auth_header(mut self, header: impl Into<String>) -> Self {
        self.auth_header = header.into();
        self
    }

    pub fn with_timeout(mut self, timeout: Duration) -> Self {
        self.timeout = timeout;
        self
    }

    /// Build a request path from caller-supplied segments and query pairs.
    ///
    /// Each segment is percent-encoded as a single URL path segment, so reserved
    /// characters such as `?`, `#`, `/` and `.` cannot inject query parameters
    /// or traverse the path (e.g. via `../`). Query values are encoded with the
    /// standard form encoder. The returned string always starts with `/`.
    fn encoded_path(segments: &[&str], query: &[(&str, &str)]) -> String {
        // A constant, absolute base lets `url` apply correct path-segment
        // quoting (including the special handling of "." and ".." segments).
        let mut url = Url::parse("http://cortex.invalid/").expect("constant base URL is valid");
        {
            let mut path = url
                .path_segments_mut()
                .expect("http base URL always has path segments");
            path.pop_if_empty(); // drop the base's trailing-slash empty segment
            for segment in segments {
                path.push(segment);
            }
        }
        for (key, value) in query {
            url.query_pairs_mut().append_pair(key, value);
        }

        let mut path = url.path().to_string();
        if let Some(query) = url.query() {
            path.push('?');
            path.push_str(query);
        }
        path
    }

    fn get(&self, path: &str) -> io::Result<serde_json::Value> {
        let url = format!("{}{}", self.endpoint, path);
        let mut request = ureq::get(&url).timeout(self.timeout);
        if let Some(key) = &self.api_key {
            request = request.set(&self.auth_header, key);
        }
        let response = request.call().map_err(io_err)?;
        let body: serde_json::Value = response.into_json().map_err(io_err)?;
        Ok(body)
    }

    fn post(&self, path: &str, body: &serde_json::Value) -> io::Result<serde_json::Value> {
        let url = format!("{}{}", self.endpoint, path);
        let mut request = ureq::post(&url)
            .set("Content-Type", "application/json")
            .timeout(self.timeout);
        if let Some(key) = &self.api_key {
            request = request.set(&self.auth_header, key);
        }
        let response = request.send_json(body).map_err(io_err)?;
        let resp_body: serde_json::Value = response.into_json().map_err(io_err)?;
        Ok(resp_body)
    }

    /// Check if cortex is healthy.
    pub fn health(&self) -> bool {
        self.get("/healthz")
            .ok()
            .and_then(|v| v.get("status").and_then(|s| s.as_str().map(|s| s == "ok")))
            .unwrap_or(false)
    }

    /// Check if cortex is ready to accept requests.
    pub fn ready(&self) -> bool {
        self.get("/readyz")
            .ok()
            .map(|v| {
                v.get("status").and_then(|s| s.as_str()) == Some("ok")
                    || v.get("ready").and_then(|r| r.as_bool()) == Some(true)
            })
            .unwrap_or(false)
    }

    /// Fetch Prometheus metrics from cortex.
    pub fn metrics(&self) -> io::Result<String> {
        let url = format!("{}/metrics", self.endpoint);
        let response = ureq::get(&url)
            .timeout(self.timeout)
            .call()
            .map_err(io_err)?;
        let body = response.into_string().map_err(io_err)?;
        Ok(body)
    }

    /// Reconstruct an incident timeline with root cause analysis.
    pub fn reconstruct(&self, incident_id: &str, mode: &str) -> io::Result<IncidentContext> {
        let body = serde_json::json!({
            "incident_id": incident_id,
            "mode": mode,
        });
        let resp = self.post("/reconstruct", &body)?;
        let ctx: IncidentContext = serde_json::from_value(resp).map_err(io_err)?;
        Ok(ctx)
    }

    /// Reconstruct an incident using the URL-param variant.
    pub fn reconstruct_incident(
        &self,
        incident_id: &str,
        mode: &str,
    ) -> io::Result<IncidentContext> {
        let path = Self::encoded_path(&["incidents", incident_id, "reconstruct"], &[]);
        let body = serde_json::json!({"mode": mode});
        let resp = self.post(&path, &body)?;
        let ctx: IncidentContext = serde_json::from_value(resp).map_err(io_err)?;
        Ok(ctx)
    }

    /// Fetch the dependency graph for a service.
    pub fn service_graph(&self, service: &str, depth: u32) -> io::Result<GraphView> {
        let depth = depth.to_string();
        let path = Self::encoded_path(&["graph", "service", service], &[("depth", &depth)]);
        let resp = self.get(&path)?;
        let graph: GraphView = serde_json::from_value(resp).map_err(io_err)?;
        Ok(graph)
    }

    /// Fetch the graph for a specific incident.
    pub fn incident_graph(&self, incident_id: &str, depth: u32) -> io::Result<GraphView> {
        let depth = depth.to_string();
        let path = Self::encoded_path(&["graph", "incident", incident_id], &[("depth", &depth)]);
        let resp = self.get(&path)?;
        let graph: GraphView = serde_json::from_value(resp).map_err(io_err)?;
        Ok(graph)
    }

    /// Record a remediation action taken for an incident.
    pub fn record_remediation(&self, remediation: &Remediation) -> io::Result<()> {
        let body = serde_json::json!({
            "incident_id": remediation.incident_id,
            "action": remediation.action,
            "operator": remediation.operator,
            "attributes": remediation.attributes,
        });
        self.post("/feedback/remediation", &body)?;
        Ok(())
    }

    /// Record feedback on whether a remediation was successful.
    pub fn record_feedback(&self, feedback: &RemediationFeedback) -> io::Result<()> {
        let body = serde_json::json!({
            "remediation_id": feedback.remediation_id,
            "incident_id": feedback.incident_id,
            "outcome": feedback.outcome,
            "time_to_resolve_seconds": feedback.time_to_resolve_seconds,
            "notes": feedback.notes,
        });
        self.post("/feedback/incident", &body)?;
        Ok(())
    }

    /// Find incidents similar to the given one.
    pub fn similar_incidents(&self, incident_id: &str) -> io::Result<Vec<serde_json::Value>> {
        let body = serde_json::json!({
            "incident_id": incident_id,
            "mode": "fast",
        });
        let resp = self.post("/reconstruct", &body)?;
        let similar = resp
            .get("similar_incidents")
            .and_then(|v| v.as_array())
            .cloned()
            .unwrap_or_default();
        Ok(similar)
    }

    /// Ingest a batch of events directly into cortex.
    pub fn ingest_batch(&self, events: &[serde_json::Value]) -> io::Result<()> {
        let body = serde_json::json!({"events": events});
        self.post("/events/batch", &body)?;
        Ok(())
    }
}

fn io_err<E: std::fmt::Display>(err: E) -> io::Error {
    io::Error::other(format!("cortex client error: {err}"))
}

#[cfg(test)]
mod tests {
    use super::CortexClient;

    #[test]
    fn encoded_path_leaves_ordinary_segments_and_query_untouched() {
        let path = CortexClient::encoded_path(&["incidents", "inc-123", "reconstruct"], &[]);
        assert_eq!(path, "/incidents/inc-123/reconstruct");

        let path = CortexClient::encoded_path(&["graph", "service", "api"], &[("depth", "1")]);
        assert_eq!(path, "/graph/service/api?depth=1");
    }

    #[test]
    fn encoded_path_prevents_query_injection() {
        let path =
            CortexClient::encoded_path(&["graph", "service", "x?admin=true"], &[("depth", "1")]);
        // Only our own `?depth=1` may introduce a query; the `?` in the
        // service value must be percent-encoded into the path segment.
        assert_eq!(
            path.matches('?').count(),
            1,
            "unexpected query separator: {path}"
        );
        assert!(path.contains("%3F"), "expected '?' to be encoded: {path}");
        assert!(path.ends_with("?depth=1"));
        assert!(!path.contains("?admin=true"));
    }

    #[test]
    fn encoded_path_prevents_fragment_and_ampersand_injection() {
        let path = CortexClient::encoded_path(&["incidents", "inc#frag&x=y", "reconstruct"], &[]);
        assert!(!path.contains('#'), "fragment must not be raw: {path}");
        assert!(path.contains("%23"), "expected '#' to be encoded: {path}");
        assert!(!path.contains('?'), "no query expected: {path}");
    }

    #[test]
    fn encoded_path_prevents_path_traversal() {
        let path =
            CortexClient::encoded_path(&["graph", "service", "../../admin"], &[("depth", "1")]);
        // Slashes inside a segment must be encoded so no extra path segments
        // (and thus no traversal) can be introduced.
        assert!(!path.contains("../"), "path traversal leaked: {path}");
        assert!(path.contains("%2F"), "expected '/' to be encoded: {path}");

        // A segment that is exactly ".." is also neutralised by the URL builder.
        let path = CortexClient::encoded_path(&["incidents", "..", "reconstruct"], &[]);
        assert!(!path.contains("/../"), "dot-dot traversal leaked: {path}");
        assert!(!path.starts_with("/reconstruct"));
    }

    #[test]
    fn encoded_path_encodes_query_values() {
        let path = CortexClient::encoded_path(&["graph", "service", "api"], &[("q", "a b&c")]);
        assert!(
            path.ends_with("?q=a+b%26c"),
            "query value not encoded: {path}"
        );
    }
}
