use super::schema::{GraphView, IncidentContext, Remediation, RemediationFeedback};

/// Validation error for cortex API requests.
#[derive(Clone, Debug)]
pub struct CortexValidationError {
    pub field: String,
    pub message: String,
}

impl std::fmt::Display for CortexValidationError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}: {}", self.field, self.message)
    }
}

impl std::error::Error for CortexValidationError {}

/// Validate an incident context before sending to cortex API.
/// Checks required fields are present and well-formed.
pub fn validate_incident_context(ctx: &IncidentContext) -> Result<(), CortexValidationError> {
    if ctx.incident_id.trim().is_empty() {
        return Err(CortexValidationError {
            field: "incident_id".to_string(),
            message: "must be non-empty".to_string(),
        });
    }
    if ctx.timestamp.trim().is_empty() {
        return Err(CortexValidationError {
            field: "timestamp".to_string(),
            message: "must be non-empty".to_string(),
        });
    }
    // All comparisons against NaN are false, so the range checks alone would
    // let a NaN confidence through validation and on to the API.
    if ctx.confidence.is_nan() || ctx.confidence < 0.0 || ctx.confidence > 1.0 {
        return Err(CortexValidationError {
            field: "confidence".to_string(),
            message: "must be between 0.0 and 1.0".to_string(),
        });
    }
    Ok(())
}

/// Validate a graph view response from cortex API.
pub fn validate_graph_view(graph: &GraphView) -> Result<(), CortexValidationError> {
    for (i, node) in graph.nodes.iter().enumerate() {
        if node.get("id").is_none() {
            return Err(CortexValidationError {
                field: format!("nodes[{i}].id"),
                message: "each node must have an id".to_string(),
            });
        }
    }
    for (i, edge) in graph.edges.iter().enumerate() {
        if edge.get("source").is_none() || edge.get("target").is_none() {
            return Err(CortexValidationError {
                field: format!("edges[{i}]"),
                message: "each edge must have source and target".to_string(),
            });
        }
    }
    Ok(())
}

/// Validate remediation before recording.
pub fn validate_remediation(remediation: &Remediation) -> Result<(), CortexValidationError> {
    if remediation.incident_id.trim().is_empty() {
        return Err(CortexValidationError {
            field: "incident_id".to_string(),
            message: "must be non-empty".to_string(),
        });
    }
    if remediation.action.trim().is_empty() {
        return Err(CortexValidationError {
            field: "action".to_string(),
            message: "must be non-empty".to_string(),
        });
    }
    Ok(())
}

/// Validate remediation feedback before recording.
pub fn validate_remediation_feedback(
    feedback: &RemediationFeedback,
) -> Result<(), CortexValidationError> {
    if feedback.remediation_id.trim().is_empty() {
        return Err(CortexValidationError {
            field: "remediation_id".to_string(),
            message: "must be non-empty".to_string(),
        });
    }
    if feedback.incident_id.trim().is_empty() {
        return Err(CortexValidationError {
            field: "incident_id".to_string(),
            message: "must be non-empty".to_string(),
        });
    }
    if feedback.outcome.trim().is_empty() {
        return Err(CortexValidationError {
            field: "outcome".to_string(),
            message: "must be non-empty".to_string(),
        });
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ctx_with_confidence(confidence: f64) -> IncidentContext {
        IncidentContext {
            incident_id: "inc-1".to_string(),
            timestamp: "2026-01-01T00:00:00Z".to_string(),
            confidence,
            ..IncidentContext::default()
        }
    }

    #[test]
    fn nan_confidence_is_rejected() {
        let err = validate_incident_context(&ctx_with_confidence(f64::NAN))
            .expect_err("NaN confidence must not pass validation");
        assert_eq!(err.field, "confidence");
    }

    #[test]
    fn valid_and_out_of_range_confidence_still_behave_as_before() {
        for confidence in [0.0, 0.5, 1.0] {
            validate_incident_context(&ctx_with_confidence(confidence))
                .unwrap_or_else(|err| panic!("{confidence} should be valid: {err}"));
        }
        for confidence in [-0.1, 1.1, f64::INFINITY, f64::NEG_INFINITY] {
            let err = validate_incident_context(&ctx_with_confidence(confidence))
                .expect_err("out-of-range confidence should be rejected");
            assert_eq!(err.field, "confidence");
        }
    }
}
