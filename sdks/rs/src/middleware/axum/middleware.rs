pub fn middleware_name() -> &'static str {
    "loza-axum"
}

#[cfg(feature = "axum")]
pub mod axum_impl {
    use crate::{Logger, Params};
    use axum::extract::{MatchedPath, Request};
    use crate::{EventContext, LozaError, Logger, Params};
    use axum::extract::Request;
    use axum::middleware::Next;
    use axum::response::Response;
    use std::sync::Arc;
    use std::time::Instant;

    // Enrichment must go through the middleware's configured logger so that its
    // settings (e.g. duplicate_policy) are honored. The free `crate::Append`
    // helper routes through the unrelated global default logger instead.
    fn append_request_attrs(
        logger: &Logger,
        ctx: &mut crate::EventContext,
        user_agent: &str,
        remote_ip: &str,
    ) {
        logger.append(ctx, crate::String("http.user_agent", user_agent));
        logger.append(ctx, crate::String("http.remote_ip", remote_ip));
    }

    fn append_response_attrs(
        logger: &Logger,
        ctx: &mut crate::EventContext,
        status: u16,
        duration_ms: i64,
    ) {
        // MASS-0803 (main): status_code belongs on the typed field so
        // status-code sampling and the schema accessor can read it; writing it
        // to generic attrs would reintroduce that regression.
        crate::middleware::record_status_code(ctx, status);
        // MASS-0804 (this PR): remaining enrichment goes through the
        // middleware's configured logger so its settings (e.g.
        // duplicate_policy) are honored.
        logger.append(ctx, crate::Int("duration_ms", duration_ms));
    }

    #[allow(dead_code)]
    pub struct LozaLayer {
        logger: Arc<Logger>,
        service_name: String,
    }

    impl LozaLayer {
        pub fn new(logger: Logger, service_name: impl Into<String>) -> Self {
            Self {
                logger: Arc::new(logger),
                service_name: service_name.into(),
            }
        }
    }

    /// Finish and emit a started event, surfacing the first failure to the caller.
    ///
    /// Both operations are attempted even if finishing fails, matching the previous
    /// behaviour. Keeping this in a helper makes the error path directly testable;
    /// the middleware cannot fail the HTTP response for a telemetry error, so it
    /// logs the returned error as a warning instead of discarding it.
    pub fn finish_and_emit(
        logger: &Logger,
        ctx: &mut EventContext,
        outcome: &str,
    ) -> Result<(), LozaError> {
        let finish_result = logger.finish(ctx, outcome);
        let emit_result = logger.emit(ctx);
        finish_result?;
        emit_result?;
        Ok(())
    }

    pub async fn loza_middleware(
        logger: Arc<Logger>,
        service_name: String,
        req: Request,
        next: Next,
    ) -> Response {
        let method = req.method().to_string();
        let path = req.uri().path().to_string();
        // Prefer the matched route template (e.g. "/users/:id") over the raw
        // request path (e.g. "/users/123") so route-based grouping and sampling
        // keep bounded cardinality. This mirrors axum's own MatchedPath guidance:
        // fall back to the literal path only when routing produced no template.
        let route = req
            .extensions()
            .get::<MatchedPath>()
            .map(|matched| matched.as_str().to_string())
            .unwrap_or_else(|| path.clone());
        let user_agent = req
            .headers()
            .get("user-agent")
            .and_then(|v| v.to_str().ok())
            .unwrap_or("")
            .to_string();
        let remote_ip = ""; // Axum doesn't expose peer addr easily in middleware

        let started = Instant::now();

        let mut params = Params::new("http.request").with_kind("http");
        params.method = Some(method);
        params.path = Some(path);
        params.route = Some(route);
        params.service = Some(service_name);
        let mut ctx = logger.start_event(params);

        append_request_attrs(&logger, &mut ctx, &user_agent, &remote_ip);

        let res = next.run(req).await;

        let status = res.status().as_u16();
        let elapsed = started.elapsed().as_millis() as u64;
        let outcome = if status >= 500 { "error" } else { "success" };
        append_response_attrs(&logger, &mut ctx, status, elapsed as i64);
        let _ = logger.finish(&mut ctx, outcome);
        let _ = logger.emit(&ctx);
        crate::Append(&mut ctx, crate::Int("status_code", status as i64));
        crate::Append(&mut ctx, crate::Int("duration_ms", elapsed as i64));
        if let Err(err) = finish_and_emit(&logger, &mut ctx, outcome) {
            tracing::warn!(error = %err, "loza-axum: failed to record http.request event");
        }

        res
    }

    #[cfg(test)]
    mod tests {
        use super::*;
        use crate::{Config, MemorySinkStore, Params, SinkConfig};

        #[test]
        fn enrichment_uses_configured_logger_duplicate_policy() {
            // The global default logger resolves duplicates with `canonical_wins`
            // (existing value wins). The middleware's configured logger uses
            // `user_wins` (incoming value wins). Enrichment must honor the
            // configured logger rather than the unrelated global default.
            crate::set_global_logger(Logger::new(
                Config::dev("global-default").with_duplicate_policy("canonical_wins"),
            ));
            let configured = Logger::new(
                Config::test("axum-enrichment")
                    .with_duplicate_policy("user_wins")
                    .with_sink(SinkConfig::Memory(MemorySinkStore::new())),
            );

            let mut ctx = configured.start_event(Params::new("http.request").with_kind("http"));
            // Seed values so duplicate resolution actually matters for the
            // attributes that remain generic (http.user_agent, duration_ms).
            configured.append(&mut ctx, crate::String("http.user_agent", "seed-agent"));
            configured.append(&mut ctx, crate::Int("duration_ms", 0));

            append_request_attrs(&configured, &mut ctx, "incoming-agent", "10.0.0.1");
            append_response_attrs(&configured, &mut ctx, 200, 5);

            assert_eq!(
                ctx.http.get("user_agent").and_then(|v| v.as_str()),
                Some("incoming-agent"),
                "http.user_agent must be appended via the configured logger"
            );
            assert_eq!(
                ctx.http.get("remote_ip").and_then(|v| v.as_str()),
                Some("10.0.0.1")
            );
            // MASS-0803 (main): status_code is recorded on the typed field,
            // not as a generic attribute.
            assert_eq!(ctx.status_code, Some(200));
            // MASS-0804 (this PR): duration_ms goes through the configured
            // logger with user_wins, so the incoming value replaces the seed.
            assert_eq!(
                ctx.attrs.get("duration_ms").and_then(|v| v.as_i64()),
                Some(5),
                "duration_ms must be appended via the configured logger"
        use crate::Config;

        fn test_logger() -> Logger {
            Logger::new(Config::test("axum-middleware"))
        }

        #[test]
        fn finish_and_emit_surfaces_finish_failure() {
            let logger = test_logger();
            let mut ctx = logger.start_event(Params::new("http.request").with_kind("http"));
            // Finish once so the helper's finish attempt fails.
            assert!(logger.finish(&mut ctx, "success").is_ok());

            let err = finish_and_emit(&logger, &mut ctx, "success").unwrap_err();
            assert!(
                matches!(err, LozaError::EventAlreadyFinished { .. }),
                "expected the finish failure to be surfaced, got {err}"
            );
        }

        #[test]
        fn finish_and_emit_surfaces_emit_failure() {
            let logger = test_logger();
            let mut ctx = logger.start_event(Params::new("http.request").with_kind("http"));
            // A pending error makes emit fail validation while finish still succeeds.
            ctx.note_error("injected emit failure");

            let err = finish_and_emit(&logger, &mut ctx, "success").unwrap_err();
            assert!(
                matches!(err, LozaError::Validation(_)),
                "expected the emit failure to be surfaced, got {err}"
            );
        }
    }
}
