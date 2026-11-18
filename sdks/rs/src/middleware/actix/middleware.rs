pub fn middleware_name() -> &'static str {
    "loza-actix"
}

#[cfg(feature = "actix")]
pub mod actix_impl {
    use crate::{Logger, Params};
    use actix_web::dev::{forward_ready, Service, ServiceRequest, ServiceResponse, Transform};
    use actix_web::Error;
    use futures_util::future::{ok, LocalBoxFuture, Ready};
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

    pub struct LozaMiddleware {
        logger: Arc<Logger>,
        service_name: String,
    }

    impl LozaMiddleware {
        pub fn new(logger: Logger, service_name: impl Into<String>) -> Self {
            Self {
                logger: Arc::new(logger),
                service_name: service_name.into(),
            }
        }
    }

    impl<S, B> Transform<S, ServiceRequest> for LozaMiddleware
    where
        S: Service<ServiceRequest, Response = ServiceResponse<B>, Error = Error>,
        S::Future: 'static,
        B: 'static,
    {
        type Response = ServiceResponse<B>;
        type Error = Error;
        type Transform = LozaService<S>;
        type InitError = ();
        type Future = Ready<Result<Self::Transform, Self::InitError>>;

        fn new_transform(&self, service: S) -> Self::Future {
            ok(LozaService {
                service,
                logger: self.logger.clone(),
                service_name: self.service_name.clone(),
            })
        }
    }

    pub struct LozaService<S> {
        service: S,
        logger: Arc<Logger>,
        service_name: String,
    }

    impl<S, B> Service<ServiceRequest> for LozaService<S>
    where
        S: Service<ServiceRequest, Response = ServiceResponse<B>, Error = Error>,
        S::Future: 'static,
        B: 'static,
    {
        type Response = ServiceResponse<B>;
        type Error = Error;
        type Future = LocalBoxFuture<'static, Result<Self::Response, Self::Error>>;

        forward_ready!(service);

        fn call(&self, req: ServiceRequest) -> Self::Future {
            let logger = self.logger.clone();
            let service_name = self.service_name.clone();
            let method = req.method().to_string();
            let path = req.path().to_string();
            let user_agent = req
                .headers()
                .get("user-agent")
                .and_then(|v| v.to_str().ok())
                .unwrap_or("")
                .to_string();
            let remote_ip = req.peer_addr().map(|a| a.to_string()).unwrap_or_default();

            let started = Instant::now();
            let fut = self.service.call(req);

            Box::pin(async move {
                let mut params = Params::new("http.request").with_kind("http");
                params.method = Some(method);
                params.path = Some(path.clone());
                params.route = Some(path);
                params.service = Some(service_name);
                let mut ctx = logger.start_event(params);

                append_request_attrs(logger.as_ref(), &mut ctx, &user_agent, &remote_ip);

                match fut.await {
                    Ok(res) => {
                        let status = res.status().as_u16();
                        let elapsed = started.elapsed().as_millis() as u64;
                        let outcome = if status >= 500 { "error" } else { "success" };
                        append_response_attrs(logger.as_ref(), &mut ctx, status, elapsed as i64);
                        let _ = logger.finish(&mut ctx, outcome);
                        let _ = logger.emit(&ctx);
                        Ok(res)
                    }
                    Err(err) => {
                        let _ = logger.finish_error(&mut ctx, err.to_string());
                        let _ = logger.emit(&ctx);
                        Err(err)
                    }
                }
            })
        }
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
                Config::test("actix-enrichment")
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
    }
}
