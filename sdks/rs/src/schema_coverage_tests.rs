#[cfg(test)]
mod tests {
    use super::super::*;
    use crate::config::SchemaConfig;
    use crate::{Attr, Params};
    use serde_json::json;

    #[test]
    fn schema_encoders_and_event_view_cover_supported_shapes() {
        let mut params = Params::new("checkout")
            .with_kind("http")
            .with_message("hello")
            .with_method("GET")
            .with_path("/items")
            .with_route("/items")
            .with_status_code(200);
        params.trace_id = Some("0123456789abcdef0123456789abcdef".into());
        params.span_id = Some("0123456789abcdef".into());
        params.request_id = Some("request".into());
        let mut event = EventContext::new("service", params);
        event.append_attr(Attr::new("nested.value", json!(1)));
        event.append_attr(Attr::new("nested", json!({"child": true})));
        event.checkpoint("started");
        let view = EventView::new(&event);
        assert!(!view.event_id().is_empty());
        assert_eq!(view.service(), "service");
        assert_eq!(view.event(), "checkout");
        assert_eq!(view.kind(), "http");
        assert_eq!(view.level(), "info");
        assert!(view.outcome().is_none());
        assert_eq!(view.message(), Some("hello"));
        assert!(view.duration_ms().is_none());
        assert!(!view.timestamp().is_empty());
        assert_eq!(view.trace_id(), Some("0123456789abcdef0123456789abcdef"));
        assert_eq!(view.span_id(), Some("0123456789abcdef"));
        assert_eq!(view.request_id(), "request");
        assert_eq!(view.method(), Some("GET"));
        assert_eq!(view.path(), Some("/items"));
        assert_eq!(view.route(), Some("/items"));
        assert_eq!(view.status_code(), Some(200));
        assert!(view.attrs().contains_key("nested"));
        assert!(view.http().is_empty());
        assert!(view.user().is_empty());
        assert!(view.tenant().is_empty());
        assert!(view.resource().is_empty());
        assert!(view.error().is_none());
        assert_eq!(view.checkpoints().len(), 1);
        assert!(!view.is_finished());
        assert_eq!(view.lifecycle_state(), "active");
        assert_eq!(view.attr("nested.child"), Some(&json!(true)));
        assert_eq!(view.attr("nested.missing"), None);
        assert_eq!(view.attr("missing"), None);
        assert!(!format!("{view:?}").is_empty());

        let value = serde_json::to_value(&event).unwrap();
        for schema in [
            SchemaConfig::Default,
            SchemaConfig::Nested,
            SchemaConfig::Flat,
            SchemaConfig::OTel,
            SchemaConfig::ECS,
            SchemaConfig::Datadog,
        ] {
            let encoded = encode_schema(&event, &schema);
            assert!(encoded.is_object());
            assert!(encode_schema_from_value(value.clone(), &schema).is_object());
        }
        let custom = SchemaConfig::Custom(std::sync::Arc::new(CustomSchema));
        assert_eq!(encode_schema(&event, &custom), json!({"custom": true}));
        assert_eq!(
            encode_schema_from_value(json!({"event_id": "id"}), &custom),
            json!({"event_id": "id"})
        );
        let non_object = json!("value");
        assert_eq!(
            encode_schema_from_value(non_object.clone(), &SchemaConfig::Default),
            non_object
        );
        let normalized = encode_schema_from_value(
            json!({"method": "GET", "http": {"path": "/"}}),
            &SchemaConfig::Default,
        );
        assert_eq!(normalized["http"]["method"], "GET");
        assert_eq!(normalized["http"]["path"], "/");
        let flat = encode_schema_from_value(
            json!({"outer": {"inner": 1}, "empty": {}}),
            &SchemaConfig::Flat,
        );
        assert_eq!(flat["outer_inner"], 1);
        assert!(!flat.as_object().unwrap().contains_key("empty"));
    }

    #[test]
    fn ecs_duration_converts_milliseconds_to_bounded_nanoseconds() {
        let encoded = encode_schema_from_value(
            json!({"duration_ms": 7}),
            &SchemaConfig::ECS,
        );
        assert_eq!(encoded["event"]["duration"], json!(7_000_000));

        let overflow = encode_schema_from_value(
            json!({"duration_ms": u64::MAX}),
            &SchemaConfig::ECS,
        );
        assert_eq!(overflow["event"]["duration"], json!(i64::MAX));

        let logger = crate::New(crate::Config::test("ecs-duration")
            .with_schema(SchemaConfig::ECS));
        let mut event = EventContext::new("ecs-duration", Params::new("request"));
        event.duration_ms = Some(u128::MAX);

        let encoded = encode_schema(&event, &SchemaConfig::ECS);
        assert_eq!(encoded["event"]["duration"], json!(i64::MAX));
        assert_eq!(encoded["service"]["name"], json!("ecs-duration"));

        let mut logger_event = EventContext::new("ecs-duration", Params::new("request"));
        logger_event.duration_ms = Some(7);
        let emitted = logger.emit(&logger_event).unwrap();
        let emitted: serde_json::Value = serde_json::from_str(&emitted).unwrap();
        assert_eq!(emitted["event"]["duration"], json!(7_000_000));

        let mut overflow_event = EventContext::new("ecs-duration", Params::new("request"));
        overflow_event.duration_ms = Some(u128::MAX);
        let emitted = logger.emit(&overflow_event).unwrap();
        let emitted: serde_json::Value = serde_json::from_str(&emitted).unwrap();
        assert_eq!(emitted["event"]["duration"], json!(i64::MAX));
        assert_eq!(emitted["service"]["name"], json!("ecs-duration"));
    }

    #[test]
    fn otel_schema_converts_rfc3339_timestamp_to_unix_nanos() {
        let mut event = EventContext::new("service", Params::new("checkout"));
        event.timestamp = "2026-11-17T00:00:00.123456789Z".into();
        let encoded = encode_schema(&event, &SchemaConfig::OTel);
        // OTLP JSON encodes fixed64 fields such as time_unix_nano as decimal strings.
        assert_eq!(encoded["time_unix_nano"], json!("1794873600123456789"));
        assert!(encoded.get("timestamp").is_none());
    }

    #[test]
    fn otel_schema_timestamp_handles_offsets_and_missing_or_invalid_values() {
        let with_offset = encode_schema_from_value(
            json!({"timestamp": "2026-11-17T05:30:00+05:30"}),
            &SchemaConfig::OTel,
        );
        assert_eq!(with_offset["time_unix_nano"], json!("1794873600000000000"));

        let invalid =
            encode_schema_from_value(json!({"timestamp": "not-a-timestamp"}), &SchemaConfig::OTel);
        assert!(invalid["time_unix_nano"].is_null());

        let missing = encode_schema_from_value(json!({}), &SchemaConfig::OTel);
        assert!(missing["time_unix_nano"].is_null());
    }

    struct CustomSchema;

    impl Schema for CustomSchema {
        fn encode(&self, _event: &EventContext) -> Value {
            json!({"custom": true})
        }
    }
}
