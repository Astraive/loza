use loza::cortex;

/// Regression test for MASS-0787.
///
/// The legacy `cortex::models` path, the canonical `cortex::schema` path and
/// the flat re-exports at `cortex::*` must all name the *same* types. This
/// fails to compile if a duplicate set of struct definitions is ever
/// reintroduced under `models.rs` and allowed to drift from `schema.rs`.
#[test]
fn legacy_models_path_is_a_single_canonical_definition() {
    fn same_type<T>(_: &T, _: &T) {}

    let models_context: cortex::models::IncidentContext = Default::default();
    let schema_context: cortex::schema::IncidentContext = models_context.clone();
    let flat_context: cortex::IncidentContext = schema_context.clone();
    same_type(&models_context, &schema_context);
    same_type(&schema_context, &flat_context);

    let models_graph: cortex::models::GraphView = Default::default();
    let schema_graph: cortex::schema::GraphView = models_graph.clone();
    let flat_graph: cortex::GraphView = schema_graph.clone();
    same_type(&models_graph, &schema_graph);
    same_type(&schema_graph, &flat_graph);

    let models_remediation: cortex::models::Remediation = Default::default();
    let schema_remediation: cortex::schema::Remediation = models_remediation.clone();
    let flat_remediation: cortex::Remediation = schema_remediation.clone();
    same_type(&models_remediation, &schema_remediation);
    same_type(&schema_remediation, &flat_remediation);

    let models_feedback: cortex::models::RemediationFeedback = Default::default();
    let schema_feedback: cortex::schema::RemediationFeedback = models_feedback.clone();
    let flat_feedback: cortex::RemediationFeedback = schema_feedback.clone();
    same_type(&models_feedback, &schema_feedback);
    same_type(&schema_feedback, &flat_feedback);
}

/// Values created through the legacy path must serialize identically to the
/// canonical schema path, proving no field-level drift between the two.
#[test]
fn legacy_models_path_round_trips_with_canonical_schema() {
    let context: cortex::models::IncidentContext = cortex::models::IncidentContext {
        incident_id: "inc-0787".to_string(),
        confidence: 0.75,
        related_services: vec!["api".to_string(), "worker".to_string()],
        ..Default::default()
    };

    let json = serde_json::to_value(&context).expect("serialize legacy model");

    let via_schema: cortex::schema::IncidentContext =
        serde_json::from_value(json.clone()).expect("deserialize into canonical schema model");
    let via_flat: cortex::IncidentContext =
        serde_json::from_value(json).expect("deserialize into flat re-exported model");

    assert_eq!(via_schema.incident_id, "inc-0787");
    assert_eq!(via_flat.incident_id, "inc-0787");
    assert_eq!(via_schema.confidence, 0.75);
    assert_eq!(via_flat.confidence, 0.75);
    assert_eq!(via_schema.related_services, context.related_services);
    assert_eq!(via_flat.related_services, context.related_services);
}
