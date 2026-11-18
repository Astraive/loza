pub mod actix;
pub mod axum;
pub mod hyper;
pub mod tower;

pub use tower::{MiddlewareConfig, MiddlewareResult};

/// Record an HTTP response status on the event's typed `status_code` field.
///
/// HTTP middleware must use this instead of appending a generic `status_code`
/// attribute. Consumers such as status-code sampling
/// (`sampling::should_sample`) and the schema `status_code()` accessor read the
/// typed field, which stays `None` when the value is only written to `attrs`.
pub(crate) fn record_status_code(ctx: &mut crate::EventContext, status: u16) {
    ctx.status_code = Some(status);
}

#[cfg(test)]
mod tests {
    use super::record_status_code;
    use crate::{Config, Int, New, Params, SamplerConfig, SinkConfig};

    fn new_event() -> crate::EventContext {
        let logger = New(Config::test("middleware-test").with_sink(SinkConfig::Noop));
        logger.start_event(Params::new("http.request").with_kind("http"))
    }

    /// Regression test for MASS-0803: a generic `status_code` attribute does
    /// NOT populate the typed field, so status-code consumers see nothing.
    #[test]
    fn generic_status_code_attr_does_not_feed_typed_consumers() {
        let logger = New(Config::test("middleware-test").with_sink(SinkConfig::Noop));
        let mut ctx = new_event();
        logger.append(&mut ctx, Int("status_code", 503));

        assert_eq!(ctx.status_code, None);
        assert_eq!(
            ctx.attrs.get("status_code").and_then(|v| v.as_u64()),
            Some(503)
        );
        let sampler = SamplerConfig::StatusCodes(vec![503]);
        assert!(
            !crate::sampling::should_sample(&ctx, &sampler),
            "generic attr must not satisfy status-code sampling"
        );
    }

    /// The middleware fix records the status in the typed field, which feeds
    /// status-code sampling and is emitted as a top-level `status_code`.
    #[test]
    fn record_status_code_sets_typed_field_and_emits_it() {
        let logger = New(Config::test("middleware-test").with_sink(SinkConfig::Noop));
        let mut ctx = new_event();
        record_status_code(&mut ctx, 503);

        assert_eq!(ctx.status_code, Some(503));
        assert!(ctx.attrs.get("status_code").is_none());

        let sampler = SamplerConfig::StatusCodes(vec![503]);
        assert!(
            crate::sampling::should_sample(&ctx, &sampler),
            "typed status_code must satisfy status-code sampling"
        );

        let _ = logger.finish(&mut ctx, "error");
        let encoded = logger.emit(&ctx).expect("emit event");
        let payload: serde_json::Value = serde_json::from_str(&encoded).expect("decode payload");
        assert_eq!(payload.get("status_code").and_then(|v| v.as_u64()), Some(503));
        assert!(
            payload
                .get("attrs")
                .and_then(|v| v.get("status_code"))
                .is_none(),
            "status_code must not be emitted as a generic attribute"
        );
    }
}
