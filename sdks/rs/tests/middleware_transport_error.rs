//! Regression test: capturing a request must surface the real `LozaError`
//! from `Logger::emit` instead of fabricating a `serde_json::Error` I/O error.

use loza::middleware::tower::capture_request;
use loza::{Config, LozaError, MemorySinkStore, SinkConfig};

#[test]
fn capture_request_returns_transport_error_as_loza_error() {
    // A paused sink makes `Logger::emit` fail with a genuine
    // `LozaError::Transport`. `capture_request` must propagate that variant.
    let sink = SinkConfig::Memory(MemorySinkStore::new());
    let logger = loza::New(Config::test("web").with_sink(sink.clone()));

    loza::Pause(&sink);
    let result = capture_request(&logger, "GET", "/health", 200);
    loza::Resume(&sink);

    let err = result.expect_err("expected a transport error from the paused sink");
    match err {
        LozaError::Transport(message) => {
            assert!(
                message.contains("delivery_failed"),
                "unexpected transport message: {message}"
            );
        }
        other => panic!("expected LozaError::Transport, got {other:?}"),
    }
}
