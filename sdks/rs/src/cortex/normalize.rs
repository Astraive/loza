use super::schema::{IncidentContext, Remediation, RemediationFeedback};
use serde_json::Value;
use time::format_description::well_known::Rfc3339;

/// Normalize an incident context for sending to cortex API.
/// Ensures timestamps are RFC3339, trims whitespace, fills defaults.
pub fn normalize_incident_context(ctx: &mut IncidentContext) {
    ctx.incident_id = ctx.incident_id.trim().to_string();
    ctx.timestamp = normalize_timestamp(&ctx.timestamp);
    ctx.confidence = ctx.confidence.clamp(0.0, 1.0);

    for service in &mut ctx.related_services {
        *service = service.trim().to_string();
    }
    ctx.related_services.retain(|s| !s.is_empty());
}

/// Normalize a remediation for sending to cortex API.
pub fn normalize_remediation(remediation: &mut Remediation) {
    remediation.incident_id = remediation.incident_id.trim().to_string();
    remediation.action = remediation.action.trim().to_string();
    remediation.operator = remediation.operator.trim().to_string();
    remediation.timestamp = normalize_timestamp(&remediation.timestamp);
}

/// Normalize remediation feedback for sending to cortex API.
pub fn normalize_remediation_feedback(feedback: &mut RemediationFeedback) {
    feedback.remediation_id = feedback.remediation_id.trim().to_string();
    feedback.incident_id = feedback.incident_id.trim().to_string();
    feedback.outcome = feedback.outcome.trim().to_string();
    feedback.notes = feedback.notes.trim().to_string();
    feedback.timestamp = normalize_timestamp(&feedback.timestamp);
}

/// Normalize graph node IDs to lowercase trimmed strings.
pub fn normalize_graph_nodes(nodes: &mut [Value]) {
    for node in nodes {
        if let Some(id) = node.get("id").and_then(Value::as_str) {
            let normalized = id.trim().to_string();
            node["id"] = Value::String(normalized);
        }
    }
}

/// Normalize a timestamp string.
///
/// Valid RFC3339 strings are returned trimmed and unchanged. Empty or
/// non-RFC3339-compliant values are rejected and replaced with the current
/// UTC time, so the value sent to the API is always RFC3339.
fn normalize_timestamp(ts: &str) -> String {
    let trimmed = ts.trim();
    if !trimmed.is_empty() && time::OffsetDateTime::parse(trimmed, &Rfc3339).is_ok() {
        return trimmed.to_string();
    }
    // Empty or malformed timestamp: use current UTC time.
    time::OffsetDateTime::now_utc()
        .format(&Rfc3339)
        .unwrap_or_else(|_| "1970-01-01T00:00:00Z".to_string())
}

#[cfg(test)]
mod tests {
    use super::normalize_timestamp;
    use crate::cortex::schema::{IncidentContext, Remediation, RemediationFeedback};
    use time::format_description::well_known::Rfc3339;

    fn is_rfc3339(value: &str) -> bool {
        time::OffsetDateTime::parse(value, &Rfc3339).is_ok()
    }

    #[test]
    fn valid_rfc3339_is_passed_through_unchanged() {
        let ts = "2024-01-02T03:04:05Z";
        assert_eq!(normalize_timestamp(ts), ts);
    }

    #[test]
    fn valid_rfc3339_with_offset_is_trimmed_and_preserved() {
        let ts = "2024-01-02T03:04:05+02:00";
        assert_eq!(normalize_timestamp(&format!("  {ts}  ")), ts);
    }

    #[test]
    fn malformed_timestamp_is_replaced_with_rfc3339() {
        let out = normalize_timestamp("not-a-date");
        assert_ne!(out, "not-a-date");
        assert!(is_rfc3339(&out), "expected RFC3339, got {out:?}");
    }

    #[test]
    fn empty_timestamp_is_replaced_with_rfc3339() {
        let out = normalize_timestamp("   ");
        assert!(is_rfc3339(&out), "expected RFC3339, got {out:?}");
    }

    #[test]
    fn normalizers_always_produce_rfc3339_timestamps() {
        let mut ctx = IncidentContext {
            timestamp: "not-a-date".into(),
            ..Default::default()
        };
        super::normalize_incident_context(&mut ctx);
        assert!(is_rfc3339(&ctx.timestamp), "got {:?}", ctx.timestamp);

        let mut remediation = Remediation {
            timestamp: "garbage".into(),
            ..Default::default()
        };
        super::normalize_remediation(&mut remediation);
        assert!(
            is_rfc3339(&remediation.timestamp),
            "got {:?}",
            remediation.timestamp
        );

        let mut feedback = RemediationFeedback {
            timestamp: "garbage".into(),
            ..Default::default()
        };
        super::normalize_remediation_feedback(&mut feedback);
        assert!(
            is_rfc3339(&feedback.timestamp),
            "got {:?}",
            feedback.timestamp
        );
    }
}
