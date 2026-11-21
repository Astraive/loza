use crate::{EventContext, Logger, LozaError, Params};

/// Finish a request event, propagating the real [`LozaError`] from `finish`.
fn finish_request(
    logger: &Logger,
    ctx: &mut EventContext,
    status_code: u16,
) -> Result<(), LozaError> {
    let outcome = if status_code >= 500 {
        "error"
    } else {
        "success"
    };
    logger.finish(ctx, outcome)
}

#[cfg(feature = "tower")]
use crate::{Append, Int, String as AttrString};
#[cfg(feature = "tower")]
use futures_util::future::BoxFuture;
#[cfg(feature = "tower")]
use futures_util::FutureExt;
#[cfg(feature = "tower")]
use http::{header::USER_AGENT, Request, Response, StatusCode};
#[cfg(feature = "tower")]
use std::panic::AssertUnwindSafe;
#[cfg(feature = "tower")]
use std::task::{Context, Poll};
#[cfg(feature = "tower")]
use std::time::Instant;
#[cfg(feature = "tower")]
use tower::Service;

#[derive(Clone, Debug)]
pub struct MiddlewareConfig {
    pub service: String,
    pub route: Option<String>,
    pub recover_panics: bool,
}

impl Default for MiddlewareConfig {
    fn default() -> Self {
        Self {
            service: String::new(),
            route: None,
            recover_panics: true,
        }
    }
}

#[derive(Clone, Debug)]
pub struct MiddlewareResult {
    pub event_id: String,
    pub encoded: String,
}

/// Captures an HTTP request as a wide event and returns its encoded payload.
///
/// # Errors
///
/// Returns [`LozaError`] when the event cannot be finished or emitted — most
/// commonly [`LozaError::Transport`] when delivery to a configured sink fails.
///
/// # Migration note
///
/// This function no longer returns `serde_json::Error`: no JSON (de)serialization
/// occurs here, and that type only masked the real [`LozaError`] failure mode.
/// Callers that matched on the old error type should match on [`LozaError`]
/// variants instead; callers using `?` into `Box<dyn Error>`, `.unwrap()`, or
/// `.map()` need no change.
pub fn capture_request(
    logger: &Logger,
    method: &str,
    path: &str,
    status_code: u16,
) -> Result<MiddlewareResult, LozaError> {
    let mut params = Params::new(format!("{method} {path}")).with_kind("http");
    params.method = Some(method.to_string());
    params.path = Some(path.to_string());
    params.status_code = Some(status_code);
    let mut ctx = logger.start_event(params);
    finish_request(logger, &mut ctx, status_code)?;
    let event_id = ctx.event_id.clone();
    let encoded = logger.emit(&ctx)?;
    Ok(MiddlewareResult { event_id, encoded })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::test;

    #[test]
    fn finish_request_propagates_finish_errors() {
        let logger = test("web");
        let mut ctx = logger.start_event(Params::new("GET /health").with_kind("http"));

        finish_request(&logger, &mut ctx, 200).expect("first finish should succeed");

        let err = finish_request(&logger, &mut ctx, 500)
            .expect_err("a second finish must surface the error instead of being swallowed");
        assert!(
            err.to_string().contains("already finished"),
            "expected EventAlreadyFinished, got: {err}"
        );
    }

    #[test]
    fn capture_request_finishes_and_emits_the_event() {
        let logger = test("web");
        let result =
            capture_request(&logger, "GET", "/health", 200).expect("capture should succeed");
        assert!(result.encoded.contains("\"kind\":\"http\""));
        assert!(result.encoded.contains("\"outcome\":\"success\""));
    }

    #[test]
    fn capture_request_marks_server_errors_as_errors() {
        let logger = test("web");
        let result =
            capture_request(&logger, "GET", "/broken", 503).expect("capture should succeed");
        assert!(result.encoded.contains("\"outcome\":\"error\""));
    }
}
/// Capture an HTTP request event with the middleware configuration applied.
///
/// Unlike [`capture_request`], this applies `config.service` as the emitting
/// service and `config.route` as the matched route pattern (falling back to
/// the request path). This is the capture path used by [`LozaService`].
///
/// [`LozaService`]: crate::middleware::tower::LozaService
pub fn capture_request_with_config(
    logger: &Logger,
    config: &MiddlewareConfig,
    method: &str,
    path: &str,
    status_code: u16,
) -> Result<MiddlewareResult, serde_json::Error> {
    let mut params = Params::new(format!("{method} {path}")).with_kind("http");
    params.method = Some(method.to_string());
    params.path = Some(path.to_string());
    params.route = Some(config.route.clone().unwrap_or_else(|| path.to_string()));
    if !config.service.is_empty() {
        params.service = Some(config.service.clone());
    }
    params.status_code = Some(status_code);
    let mut ctx = logger.start_event(params);
    let _ = logger.finish(
        &mut ctx,
        if status_code >= 500 {
            "error"
        } else {
            "success"
        },
    );
    let event_id = ctx.event_id.clone();
    let encoded = logger
        .emit(&ctx)
        .map_err(|err| serde_json::Error::io(std::io::Error::other(err.to_string())))?;
    Ok(MiddlewareResult { event_id, encoded })
}

/// Tower service produced by [`LozaLayer`](crate::middleware::tower::LozaLayer).
///
/// It records one `http` event per request. `MiddlewareConfig::service` and
/// `MiddlewareConfig::route` are applied to the emitted event, and
/// `MiddlewareConfig::recover_panics` controls whether a panic in the inner
/// service is converted into an HTTP 500 response (the default) or propagated.
#[cfg(feature = "tower")]
#[derive(Clone)]
pub struct LozaService<S> {
    inner: S,
    config: MiddlewareConfig,
    logger: Logger,
}

#[cfg(feature = "tower")]
impl<S> LozaService<S> {
    pub fn new(inner: S, config: MiddlewareConfig, logger: Logger) -> Self {
        Self {
            inner,
            config,
            logger,
        }
    }
}

#[cfg(feature = "tower")]
impl<S, B, RespBody> Service<Request<B>> for LozaService<S>
where
    S: Service<Request<B>, Response = Response<RespBody>> + Send + 'static,
    S::Future: Send + 'static,
    S::Error: Send + 'static,
    B: Send + 'static,
    RespBody: Default + Send + 'static,
{
    type Response = Response<RespBody>;
    type Error = S::Error;
    type Future = BoxFuture<'static, Result<Self::Response, Self::Error>>;

    fn poll_ready(&mut self, cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        self.inner.poll_ready(cx)
    }

    fn call(&mut self, req: Request<B>) -> Self::Future {
        let method = req.method().to_string();
        let path = req.uri().path().to_string();
        let user_agent = req
            .headers()
            .get(USER_AGENT)
            .and_then(|v| v.to_str().ok())
            .unwrap_or("")
            .to_string();
        let started = Instant::now();

        let config = self.config.clone();
        let logger = self.logger.clone();
        let fut = self.inner.call(req);

        Box::pin(async move {
            let result = if config.recover_panics {
                match AssertUnwindSafe(fut).catch_unwind().await {
                    Ok(result) => result,
                    Err(_) => {
                        emit_http_event(
                            &logger,
                            &config,
                            HttpEventData {
                                method: &method,
                                path: &path,
                                user_agent: &user_agent,
                                status_code: 500,
                                duration_ms: started.elapsed().as_millis() as u64,
                                outcome: "error",
                            },
                        );
                        return Ok(Response::builder()
                            .status(StatusCode::INTERNAL_SERVER_ERROR)
                            .body(RespBody::default())
                            .expect("a 500 response with a default body is valid"));
                    }
                }
            } else {
                fut.await
            };

            match result {
                Ok(res) => {
                    let status = res.status().as_u16();
                    emit_http_event(
                        &logger,
                        &config,
                        HttpEventData {
                            method: &method,
                            path: &path,
                            user_agent: &user_agent,
                            status_code: status,
                            duration_ms: started.elapsed().as_millis() as u64,
                            outcome: if status >= 500 { "error" } else { "success" },
                        },
                    );
                    Ok(res)
                }
                Err(err) => {
                    emit_http_event(
                        &logger,
                        &config,
                        HttpEventData {
                            method: &method,
                            path: &path,
                            user_agent: &user_agent,
                            status_code: 500,
                            duration_ms: started.elapsed().as_millis() as u64,
                            outcome: "error",
                        },
                    );
                    Err(err)
                }
            }
        })
    }
}

#[cfg(feature = "tower")]
struct HttpEventData<'a> {
    method: &'a str,
    path: &'a str,
    user_agent: &'a str,
    status_code: u16,
    duration_ms: u64,
    outcome: &'a str,
}

#[cfg(feature = "tower")]
fn emit_http_event(logger: &Logger, config: &MiddlewareConfig, event: HttpEventData<'_>) {
    let HttpEventData {
        method,
        path,
        user_agent,
        status_code,
        duration_ms,
        outcome,
    } = event;
    let mut params = Params::new("http.request").with_kind("http");
    params.method = Some(method.to_string());
    params.path = Some(path.to_string());
    params.route = Some(config.route.clone().unwrap_or_else(|| path.to_string()));
    if !config.service.is_empty() {
        params.service = Some(config.service.clone());
    }
    params.status_code = Some(status_code);

    let mut ctx = logger.start_event(params);
    Append(
        &mut ctx,
        AttrString("http.user_agent", user_agent.to_string()),
    );
    Append(&mut ctx, Int("duration_ms", duration_ms as i64));
    let _ = logger.finish(&mut ctx, outcome);
    let _ = logger.emit(&ctx);
}
