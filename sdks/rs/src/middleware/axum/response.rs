/// Captures the status and body size of an HTTP response for telemetry.
///
/// The body size is read from the response body's size hint, so capturing it
/// never consumes or buffers the body.
#[derive(Clone, Debug, Default)]
pub struct ResponseCapture {
    pub status: u16,
    pub bytes: usize,
}

impl ResponseCapture {
    /// Start a capture for a response with the given status code.
    pub fn new(status: u16) -> Self {
        Self { status, bytes: 0 }
    }

    /// Accumulate response body bytes without overwriting the captured status.
    pub fn record_body_bytes(&mut self, bytes: usize) {
        self.bytes = self.bytes.saturating_add(bytes);
    }

    /// The captured HTTP status code.
    pub fn status(&self) -> u16 {
        self.status
    }

    /// The captured response body size in bytes.
    pub fn bytes(&self) -> usize {
        self.bytes
    }
}

#[cfg(feature = "axum")]
impl ResponseCapture {
    /// Capture status and body size from an axum response without consuming it.
    ///
    /// An exact body size is used when the body advertises one; otherwise the
    /// body's lower size bound is recorded, which is still useful for streamed
    /// responses and never requires buffering the payload.
    pub fn from_response<B>(res: &axum::response::Response<B>) -> Self
    where
        B: axum::body::HttpBody,
    {
        let mut capture = Self::new(res.status().as_u16());
        let hint = res.body().size_hint();
        let bytes = hint.exact().unwrap_or_else(|| hint.lower());
        capture.record_body_bytes(usize::try_from(bytes).unwrap_or(usize::MAX));
        capture
    }
}

#[cfg(all(test, feature = "axum"))]
mod tests {
    use axum::body::{Body, HttpBody as _};
    use axum::response::Response;

    use super::ResponseCapture;

    #[test]
    fn record_body_bytes_accumulates_without_changing_status() {
        let mut capture = ResponseCapture::new(201);
        capture.record_body_bytes(4);
        capture.record_body_bytes(6);
        assert_eq!(capture.status(), 201);
        assert_eq!(capture.bytes(), 10);
    }

    #[test]
    fn byte_count_saturates_instead_of_overflowing() {
        let mut capture = ResponseCapture::new(200);
        capture.record_body_bytes(usize::MAX);
        capture.record_body_bytes(1);
        assert_eq!(capture.bytes(), usize::MAX);
        assert_eq!(capture.status(), 200);
    }

    #[test]
    fn from_response_captures_status_and_body_size() {
        let res = Response::builder()
            .status(404)
            .body(Body::from("not found"))
            .unwrap();
        let capture = ResponseCapture::from_response(&res);
        assert_eq!(capture.status(), 404);
        assert_eq!(capture.bytes(), 9);
    }

    #[test]
    fn from_response_leaves_body_readable() {
        let res = Response::new(Body::from("hello"));
        let capture = ResponseCapture::from_response(&res);
        assert_eq!(capture.bytes(), 5);
        assert_eq!(res.body().size_hint().exact(), Some(5));
    }
}
