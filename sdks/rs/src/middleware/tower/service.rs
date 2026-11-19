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
