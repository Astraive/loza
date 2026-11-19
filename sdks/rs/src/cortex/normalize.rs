use super::schema::{IncidentContext, Remediation, RemediationFeedback};
use serde_json::Value;
use time::format_description::well_known::Rfc3339;

/// Normalize an incident context for sending to cortex API.
/// Ensures timestamps are RFC3339, trims whitespace, fills defaults.
pub fn normalize_incident_context(ctx: &mut IncidentContext) {
    ctx.incident_id = ctx.incident_id.trim().to_string();
    ctx.timestamp = normalize_timestamp(&ctx.timestamp);
    // `clamp` leaves NaN untouched (both range comparisons are false), so a
    // NaN confidence would survive normalization and be sent to the API.
    // Map it to the default confidence before clamping. Infinities are still
    // handled by `clamp` itself (+Inf -> 1.0, -Inf -> 0.0).
    ctx.confidence = if ctx.confidence.is_nan() {
        0.0
    } else {
        ctx.confidence.clamp(0.0, 1.0)
    };

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
            let normalized = id.trim().to_lowercase();
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
    use super::normalize_graph_nodes;
    use super::normalize_timestamp;
    use crate::cortex::schema::{IncidentContext, Remediation, RemediationFeedback};
    use serde_json::{json, Value};
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

    #[test]
    fn normalizes_graph_node_ids_to_lowercase_trimmed_strings() {
        let mut nodes: Vec<Value> = vec![
            json!({"id": "  Service-ABC  "}),
            json!({"id": "MiXeD_Node"}),
        ];

        normalize_graph_nodes(&mut nodes);

        assert_eq!(nodes[0]["id"], Value::String("service-abc".to_string()));
        assert_eq!(nodes[1]["id"], Value::String("mixed_node".to_string()));
    }

    #[test]
    fn leaves_non_string_and_missing_ids_untouched() {
        let mut nodes: Vec<Value> = vec![json!({"id": 42}), json!({"name": "no-id"})];

        normalize_graph_nodes(&mut nodes);

        assert_eq!(nodes[0]["id"], json!(42));
        assert_eq!(nodes[1], json!({"name": "no-id"}));
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::cortex::validate::validate_incident_context;
    use serde_json::json;

    fn ctx_with_confidence(confidence: f64) -> IncidentContext {
        IncidentContext {
            incident_id: "inc-1".to_string(),
            timestamp: "2026-01-01T00:00:00Z".to_string(),
            confidence,
            ..IncidentContext::default()
        }
    }

    #[test]
    fn nan_confidence_is_normalized_to_zero() {
        let mut ctx = ctx_with_confidence(f64::NAN);

        normalize_incident_context(&mut ctx);

        assert_eq!(ctx.confidence, 0.0);
        assert!(ctx.confidence.is_finite());
    }

    #[test]
    fn finite_confidence_is_still_clamped() {
        let mut high = ctx_with_confidence(1.5);
        normalize_incident_context(&mut high);
        assert_eq!(high.confidence, 1.0);

        let mut low = ctx_with_confidence(-0.5);
        normalize_incident_context(&mut low);
        assert_eq!(low.confidence, 0.0);

        let mut normal = ctx_with_confidence(0.42);
        normalize_incident_context(&mut normal);
        assert_eq!(normal.confidence, 0.42);
    }

    #[test]
    fn normalized_nan_confidence_passes_validation_and_serializes_as_a_number() {
        let mut ctx = ctx_with_confidence(f64::NAN);

        normalize_incident_context(&mut ctx);
        validate_incident_context(&ctx).expect("normalized context must be valid");

        let value = serde_json::to_value(&ctx).expect("normalized context must serialize");
        assert_eq!(value["confidence"], json!(0.0));
    }
}
