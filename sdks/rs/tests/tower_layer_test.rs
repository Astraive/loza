#![cfg(feature = "tower")]

use http::{Request, Response, StatusCode};
use loza::middleware::tower::{
    capture_request_with_config, LozaLayer, LozaService, MiddlewareConfig,
};
use loza::testkit::testkit;
use loza::{Config, New};
use std::future::Future;
use std::pin::Pin;
use std::sync::Arc;
use std::task::{Context, Poll, Wake, Waker};
use tower::{Layer, Service};

/// Minimal executor: every future exercised here becomes ready on first poll.
struct NoopWaker;
impl Wake for NoopWaker {
    fn wake(self: Arc<Self>) {}
}

fn block_on<F: Future>(future: F) -> F::Output {
    let waker = Waker::from(Arc::new(NoopWaker));
    let mut cx = Context::from_waker(&waker);
    let mut future = Box::pin(future);
    loop {
        match future.as_mut().poll(&mut cx) {
            Poll::Ready(value) => return value,
            Poll::Pending => panic!("test future unexpectedly pending"),
        }
    }
}

#[derive(Clone)]
struct EchoService {
    status: StatusCode,
}

impl Service<Request<()>> for EchoService {
    type Response = Response<String>;
    type Error = std::convert::Infallible;
    type Future = std::future::Ready<Result<Self::Response, Self::Error>>;

    fn poll_ready(&mut self, _cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        Poll::Ready(Ok(()))
    }

    fn call(&mut self, _req: Request<()>) -> Self::Future {
        std::future::ready(Ok(Response::builder()
            .status(self.status)
            .body(String::new())
            .unwrap()))
    }
}

#[derive(Clone)]
struct PanicService;

impl Service<Request<()>> for PanicService {
    type Response = Response<String>;
    type Error = std::convert::Infallible;
    type Future = Pin<Box<dyn Future<Output = Result<Self::Response, Self::Error>> + Send>>;

    fn poll_ready(&mut self, _cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        Poll::Ready(Ok(()))
    }

    fn call(&mut self, _req: Request<()>) -> Self::Future {
        Box::pin(async { panic!("inner service failed") })
    }
}

fn test_config() -> MiddlewareConfig {
    MiddlewareConfig {
        service: "checkout".into(),
        route: Some("/orders/:id".into()),
        recover_panics: true,
    }
}

#[test]
fn config_fields_are_applied_to_captured_event() {
    let logger = New(Config::test("tower-capture"));
    let result = capture_request_with_config(&logger, &test_config(), "GET", "/orders/42", 200)
        .expect("event should be emitted");
    let value: serde_json::Value = serde_json::from_str(&result.encoded).unwrap();
    assert_eq!(value["service"], "checkout");
    assert_eq!(value["route"], "/orders/:id");
    assert_eq!(value["method"], "GET");
    assert_eq!(value["path"], "/orders/42");
}

#[test]
fn loza_layer_is_a_real_tower_layer_and_preserves_the_response() {
    // Compile-time and runtime proof that the former dead stub now implements
    // `tower::Layer` and produces a usable `Service`.
    let layer = LozaLayer::new(test_config());
    let mut service = layer.layer(EchoService {
        status: StatusCode::OK,
    });
    let req = Request::builder().uri("/orders/42").body(()).unwrap();
    let response = block_on(service.call(req)).unwrap();
    assert_eq!(response.status(), StatusCode::OK);
}

#[test]
fn loza_service_applies_config_and_emits_an_event() {
    let (logger, store) = testkit("tower-service");
    let mut service = LozaService::new(
        EchoService {
            status: StatusCode::OK,
        },
        test_config(),
        logger.clone(),
    );
    let req = Request::builder()
        .uri("/orders/42")
        .header("user-agent", "test-agent")
        .body(())
        .unwrap();
    let response = block_on(service.call(req)).unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let _ = logger.flush();

    let events = store.events();
    assert_eq!(events.len(), 1, "expected exactly one emitted event");
    let value: serde_json::Value = serde_json::from_str(&events[0]).unwrap();
    assert_eq!(value["event"], "http.request");
    assert_eq!(value["service"], "checkout");
    assert_eq!(value["route"], "/orders/:id");
    assert_eq!(value["status_code"], serde_json::json!(200));
    assert!(
        events[0].contains("test-agent"),
        "user agent was not recorded"
    );
}

#[test]
fn recover_panics_converts_an_inner_panic_into_a_500_response() {
    let (logger, store) = testkit("tower-panic");
    let mut service = LozaService::new(
        PanicService,
        test_config(), // recover_panics = true
        logger.clone(),
    );
    let req = Request::builder().uri("/orders/42").body(()).unwrap();
    let response = block_on(service.call(req)).expect("panic should be recovered");
    assert_eq!(response.status(), StatusCode::INTERNAL_SERVER_ERROR);
    let _ = logger.flush();

    let events = store.events();
    assert_eq!(events.len(), 1);
    let value: serde_json::Value = serde_json::from_str(&events[0]).unwrap();
    assert_eq!(value["outcome"], "error");
}

#[test]
fn recover_panics_disabled_propagates_the_panic() {
    let (logger, _store) = testkit("tower-panic-off");
    let config = MiddlewareConfig {
        recover_panics: false,
        ..test_config()
    };
    let mut service = LozaService::new(PanicService, config, logger);
    let req = Request::builder().uri("/orders/42").body(()).unwrap();
    let result =
        std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| block_on(service.call(req))));
    assert!(
        result.is_err(),
        "panic should propagate when recovery is disabled"
    );
}
