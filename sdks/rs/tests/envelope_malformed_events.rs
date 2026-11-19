use loza::core::client::CollectorHttpClient;
use serde_json::json;

fn client() -> CollectorHttpClient {
    CollectorHttpClient::new("http://collector.example/events")
}

#[test]
fn envelope_preserves_malformed_events_instead_of_dropping_them() {
    let envelope = client().envelope(&[
        r#"{"service":"api","event":"checkout.request"}"#.to_string(),
        "not-json".to_string(),
        "{}".to_string(),
    ]);

    let events = envelope["events"].as_array().expect("events array");
    assert_eq!(events.len(), 3, "no encoded event may be silently dropped");
    assert_eq!(events[1], json!({ "malformed": "not-json" }));
    assert_eq!(envelope["source"]["service"], "api");
}

#[test]
fn try_envelope_reports_the_index_of_the_first_malformed_event() {
    let error = client()
        .try_envelope(&[r#"{"event":"ok"}"#.to_string(), "bad-json".to_string()])
        .expect_err("malformed event must be rejected");

    assert!(error.contains("index 1"), "unexpected error: {error}");
    assert!(
        error.contains("not valid JSON"),
        "unexpected error: {error}"
    );
}

#[test]
fn try_envelope_still_accepts_valid_events() {
    let envelope = client()
        .try_envelope(&[r#"{"event":"ok","service":"api"}"#.to_string()])
        .expect("valid events must be accepted");

    assert_eq!(envelope["events"].as_array().unwrap().len(), 1);
    assert_eq!(client().validate_envelope(&envelope), Ok(()));
}

#[test]
fn validate_does_not_report_malformed_events_as_valid() {
    let result = client().validate(&[r#"{"event":"ok"}"#.to_string(), "oops".to_string()]);

    let error = result.expect_err("validate must reject malformed events");
    assert!(error.contains("index 1"), "unexpected error: {error}");
}

#[test]
fn ingest_does_not_send_a_reduced_envelope_when_an_event_is_malformed() {
    // The endpoint is deliberately unreachable: a correct implementation fails
    // while parsing, before any HTTP request is attempted.
    let result = client().ingest(&["not-json".to_string()]);

    let error = result.expect_err("ingest must reject malformed events");
    assert!(
        error.contains("not valid JSON"),
        "unexpected error: {error}"
    );
}
