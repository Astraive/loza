use std::collections::BTreeMap;

/// SpanContext represents a distributed trace span for SDK-side propagation.
///
/// This is a lightweight type for creating and propagating span context
/// within the SDK. Full tracing integration (OTLP export, sampling decisions)
/// lives in the `integrations/otel` module.
#[derive(Clone, Debug, Default)]
pub struct SpanContext {
    pub trace_id: Option<String>,
    pub span_id: Option<String>,
    pub tracestate: Option<String>,
    pub baggage: BTreeMap<String, String>,
    /// W3C traceparent version captured from an incoming header. `None` falls
    /// back to `00` for contexts built locally.
    pub traceparent_version: Option<String>,
    /// W3C traceparent trace-flags captured from an incoming header. `None`
    /// falls back to `01` (sampled) for contexts built locally.
    pub traceparent_flags: Option<String>,
}

impl SpanContext {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn with_trace_id(mut self, trace_id: impl Into<String>) -> Self {
        self.trace_id = Some(trace_id.into());
        self
    }

    pub fn with_span_id(mut self, span_id: impl Into<String>) -> Self {
        self.span_id = Some(span_id.into());
        self
    }

    pub fn with_tracestate(mut self, tracestate: impl Into<String>) -> Self {
        self.tracestate = Some(tracestate.into());
        self
    }

    pub fn with_baggage(mut self, key: impl Into<String>, value: impl Into<String>) -> Self {
        self.baggage.insert(key.into(), value.into());
        self
    }

    pub fn with_traceparent_version(mut self, version: impl Into<String>) -> Self {
        self.traceparent_version = Some(version.into());
        self
    }

    pub fn with_traceparent_flags(mut self, flags: impl Into<String>) -> Self {
        self.traceparent_flags = Some(flags.into());
        self
    }

    /// Create a child span context from this context.
    /// Generates a new span_id, preserving trace_id and baggage.
    pub fn child(&self) -> Self {
        Self {
            trace_id: self.trace_id.clone(),
            span_id: Some(generate_span_id()),
            tracestate: self.tracestate.clone(),
            baggage: self.baggage.clone(),
            traceparent_version: self.traceparent_version.clone(),
            traceparent_flags: self.traceparent_flags.clone(),
        }
    }

    /// Parse a W3C traceparent header into a SpanContext.
    pub fn from_traceparent(traceparent: &str) -> Option<Self> {
        let mut parts = traceparent.trim().split('-');
        let version = parts.next()?;
        let trace_id = parts.next()?;
        let span_id = parts.next()?;
        let flags = parts.next()?;
        if parts.next().is_some() {
            return None;
        }
        if version.len() != 2
            || trace_id.len() != 32
            || span_id.len() != 16
            || flags.len() != 2
            || !trace_id.chars().all(|ch| ch.is_ascii_hexdigit())
            || !span_id.chars().all(|ch| ch.is_ascii_hexdigit())
            || !version.chars().all(|ch| ch.is_ascii_hexdigit())
            || !flags.chars().all(|ch| ch.is_ascii_hexdigit())
        {
            return None;
        }
        Some(
            Self::new()
                .with_trace_id(trace_id.to_ascii_lowercase())
                .with_span_id(span_id.to_ascii_lowercase())
                .with_traceparent_version(version.to_ascii_lowercase())
                .with_traceparent_flags(flags.to_ascii_lowercase()),
        )
    }

    /// Format as a W3C traceparent header value.
    pub fn traceparent(&self) -> Option<String> {
        let trace_id = self.trace_id.as_deref()?;
        let span_id = self.span_id.as_deref()?;
        if trace_id.len() != 32
            || span_id.len() != 16
            || !trace_id.chars().all(|ch| ch.is_ascii_hexdigit())
            || !span_id.chars().all(|ch| ch.is_ascii_hexdigit())
        {
            return None;
        }
        let version = self.traceparent_version.as_deref().unwrap_or("00");
        let flags = self.traceparent_flags.as_deref().unwrap_or("01");
        if version.len() != 2
            || flags.len() != 2
            || !version.chars().all(|ch| ch.is_ascii_hexdigit())
            || !flags.chars().all(|ch| ch.is_ascii_hexdigit())
        {
            return None;
        }
        Some(format!(
            "{}-{}-{}-{}",
            version.to_ascii_lowercase(),
            trace_id.to_ascii_lowercase(),
            span_id.to_ascii_lowercase(),
            flags.to_ascii_lowercase()
        ))
    }
}

/// Generate a random 16-character hex span ID.
fn generate_span_id() -> String {
    let mut out = String::with_capacity(16);
    for _ in 0..16 {
        out.push_str(&format!("{:x}", fastrand::u8(0..16)));
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    const TRACE_ID: &str = "0af7651916cd43dd8448eb211c80319c";
    const SPAN_ID: &str = "b7ad6b7169203331";

    #[test]
    fn round_trips_non_default_version_and_not_sampled_flag() {
        let header = format!("01-{}-{}-00", TRACE_ID, SPAN_ID);
        let ctx = SpanContext::from_traceparent(&header).expect("valid traceparent");
        assert_eq!(ctx.traceparent().as_deref(), Some(header.as_str()));
    }

    #[test]
    fn round_trips_custom_version_and_sampled_flag() {
        let header = format!("02-{}-{}-01", TRACE_ID, SPAN_ID);
        let ctx = SpanContext::from_traceparent(&header).expect("valid traceparent");
        assert_eq!(ctx.traceparent().as_deref(), Some(header.as_str()));
    }

    #[test]
    fn locally_built_context_keeps_default_version_and_sampled_flag() {
        let ctx = SpanContext::new()
            .with_trace_id(TRACE_ID)
            .with_span_id(SPAN_ID);
        let expected = format!("00-{}-{}-01", TRACE_ID, SPAN_ID);
        assert_eq!(ctx.traceparent().as_deref(), Some(expected.as_str()));
    }

    #[test]
    fn child_preserves_parent_version_and_flags() {
        let header = format!("01-{}-{}-00", TRACE_ID, SPAN_ID);
        let ctx = SpanContext::from_traceparent(&header).expect("valid traceparent");
        let child = ctx.child();
        let child_header = child.traceparent().expect("child traceparent");
        let parts: Vec<&str> = child_header.split('-').collect();
        assert_eq!(parts[0], "01");
        assert_eq!(parts[1], TRACE_ID);
        assert_ne!(parts[2], SPAN_ID);
        assert_eq!(parts[3], "00");
    }
}
