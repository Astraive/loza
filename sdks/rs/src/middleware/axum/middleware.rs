pub fn middleware_name() -> &'static str {
    "loza-axum"
}

#[cfg(feature = "axum")]
pub mod axum_impl {
    use crate::middleware::axum::response::ResponseCapture;
    use crate::{EventContext, LozaError, Logger, Params};
    use axum::extract::{ConnectInfo, MatchedPath, Request};
    use axum::middleware::Next;
    use axum::response::Response;
    use std::convert::Infallible;
    use std::future::Future;
    use std::net::SocketAddr;
    use std::pin::Pin;
    use std::sync::Arc;
    use std::task::{Context, Poll};
    use std::time::Instant;
    use tower::{Layer, Service};

    // Enrichment must go through the middleware's configured logger so that its
    // settings (e.g. duplicate_policy) are honored. The free `crate::Append`
    // helper routes through the unrelated global default logger instead.
    fn append_request_attrs(
        logger: &Logger,
        ctx: &mut EventContext,
        user_agent: &str,
        remote_ip: &str,
    ) {
        logger.append(ctx, crate::String("http.user_agent", user_agent));
        logger.append(ctx, crate::String("http.remote_ip", remote_ip));
    }

    fn append_response_attrs(
        logger: &Logger,
        ctx: &mut EventContext,
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
    /// A usable axum/tower layer that records an `http.request` event per request.
    ///
    /// ```ignore
    /// let app = Router::new()
    ///     .route("/health", get(handler))
    ///     .layer(LozaLayer::new(logger, "checkout"));
    /// ```
    #[derive(Clone)]
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

    /// Read the peer address that axum inserts when the service is built with
    /// `into_make_service_with_connect_info::<SocketAddr>()`.
    fn remote_ip_from_request(req: &Request) -> String {
        req.extensions()
            .get::<ConnectInfo<SocketAddr>>()
            .map(|connect_info| connect_info.0.to_string())
            .unwrap_or_default()
    }

    impl<S> Layer<S> for LozaLayer {
        type Service = LozaService<S>;

        fn layer(&self, inner: S) -> Self::Service {
            LozaService {
                inner,
                logger: self.logger.clone(),
                service_name: self.service_name.clone(),
            }
        }
    }

    /// Service produced by [`LozaLayer`]; wraps the inner service with request telemetry.
    #[derive(Clone)]
    pub struct LozaService<S> {
        inner: S,
        logger: Arc<Logger>,
        service_name: String,
    }

    impl<S> Service<Request> for LozaService<S>
    where
        S: Service<Request, Response = Response> + Clone + Send + 'static,
        S::Future: Send + 'static,
    {
        type Response = Response;
        type Error = S::Error;
        type Future = Pin<Box<dyn Future<Output = Result<Response, S::Error>> + Send>>;

        fn poll_ready(&mut self, cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
            self.inner.poll_ready(cx)
        }

        fn call(&mut self, req: Request) -> Self::Future {
            // Use the ready inner service for this call, leaving a fresh clone behind.
            let clone = self.inner.clone();
            let mut inner = std::mem::replace(&mut self.inner, clone);
            let logger = self.logger.clone();
            let service_name = self.service_name.clone();
            Box::pin(run_with_telemetry(logger, service_name, req, move |req| {
                inner.call(req)
            }))
        }
    }

    /// Shared telemetry path for both the `from_fn` middleware and the tower layer.
    async fn run_with_telemetry<F, Fut, E>(
        logger: Arc<Logger>,
        service_name: String,
        req: Request,
        call: F,
    ) -> Result<Response, E>
    where
        F: FnOnce(Request) -> Fut,
        Fut: Future<Output = Result<Response, E>>,
    {
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
        let remote_ip = remote_ip_from_request(&req);

        let started = Instant::now();

        let mut params = Params::new("http.request").with_kind("http");
        params.method = Some(method);
        params.path = Some(path);
        params.route = Some(route);
        params.service = Some(service_name);
        let mut ctx = logger.start_event(params);

        append_request_attrs(&logger, &mut ctx, &user_agent, &remote_ip);

        let res = call(req).await;

        let capture = res.as_ref().ok().map(ResponseCapture::from_response);
        let status = match &res {
            Ok(response) => response.status().as_u16(),
            Err(_) => 500,
        };
        let elapsed = started.elapsed().as_millis() as u64;
        let outcome = if status >= 500 { "error" } else { "success" };
        append_response_attrs(&logger, &mut ctx, status, elapsed as i64);
        if let Some(capture) = capture {
            logger.append(&mut ctx, crate::Int("response_bytes", capture.bytes() as i64));
        }
        if let Err(err) = finish_and_emit(&logger, &mut ctx, outcome) {
            tracing::warn!(error = %err, "loza-axum: failed to record http.request event");
        }

        res
    }

    /// A usable axum middleware function that records an `http.request` event
    /// per request.
    pub async fn loza_middleware(
        logger: Arc<Logger>,
        service_name: String,
        req: Request,
        next: Next,
    ) -> Response {
        run_with_telemetry(logger, service_name, req, |req| async move {
            Ok::<_, Infallible>(next.run(req).await)
        })
        .await
        .expect("axum's Next is infallible")
    }

    #[cfg(test)]
    mod tests {
        use super::*;
        use crate::{Config, MemorySinkStore, Params, SinkConfig};

        fn test_logger() -> Logger {
            Logger::new(Config::test("axum-middleware"))
        }

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
            );
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

        #[test]
        fn remote_ip_is_read_from_connect_info_extension() {
            use axum::body::Body;
            let addr: SocketAddr = "203.0.113.42:5151".parse().unwrap();
            let mut req = Request::new(Body::empty());
            req.extensions_mut().insert(ConnectInfo(addr));
            assert_eq!(remote_ip_from_request(&req), addr.to_string());
        }

        #[test]
        fn remote_ip_defaults_to_empty_without_connect_info() {
            use axum::body::Body;
            let req = Request::new(Body::empty());
            assert_eq!(remote_ip_from_request(&req), "");
        }
    }
}
