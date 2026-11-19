#![cfg(feature = "axum")]

use axum::{body::Body, extract::Request, http::StatusCode, routing::get, Router};
use loza::middleware::axum::LozaLayer;
use serde_json::Value;
use tower::Service;

#[tokio::test]
async fn loza_layer_emits_request_event_when_layered_on_a_router() {
    let (logger, store) = loza::testkit::testkit("axum-layer-test");

    // `LozaLayer` must be a real tower::Layer that can be applied to an axum Router.
    let mut app = Router::new()
        .route("/health", get(|| async { "ok" }))
        .layer(LozaLayer::new(logger, "checkout"));

    let req = Request::builder()
        .method("GET")
        .uri("/health?verbose=1")
        .header("user-agent", "regression-test")
        .body(Body::empty())
        .expect("build request");

    std::future::poll_fn(|cx| <Router as Service<Request>>::poll_ready(&mut app, cx))
        .await
        .expect("layered router must be ready");
    let response = <Router as Service<Request>>::call(&mut app, req)
        .await
        .expect("layered router must serve the request");

    assert_eq!(response.status(), StatusCode::OK);

    let events = store.events();
    assert_eq!(
        events.len(),
        1,
        "expected exactly one emitted event, got {events:?}"
    );
    let event: Value = serde_json::from_str(&events[0]).expect("event must be valid JSON");

    assert_eq!(event["event"].as_str(), Some("http.request"));
    assert_eq!(event["service"].as_str(), Some("checkout"));
    assert_eq!(event["kind"].as_str(), Some("http"));
    assert_eq!(event["http"]["method"].as_str(), Some("GET"));
    assert_eq!(event["http"]["path"].as_str(), Some("/health"));
    assert_eq!(event["attrs"]["status_code"].as_u64(), Some(200));
    assert_eq!(event["outcome"].as_str(), Some("success"));
    assert_eq!(
        event["http"]["user_agent"].as_str(),
        Some("regression-test")
    );
}

#[tokio::test]
async fn loza_middleware_from_fn_still_emits_request_event() {
    let (logger, store) = loza::testkit::testkit("axum-from-fn-test");
    let logger = std::sync::Arc::new(logger);

    let app =
        Router::new()
            .route("/items", get(|| async { "ok" }))
            .layer(axum::middleware::from_fn(move |req, next| {
                let logger = logger.clone();
                loza::middleware::axum::loza_middleware(logger, "catalog".to_string(), req, next)
            }));

    let mut app = app;
    let req = Request::builder()
        .method("GET")
        .uri("/items")
        .body(Body::empty())
        .expect("build request");

    std::future::poll_fn(|cx| <Router as Service<Request>>::poll_ready(&mut app, cx))
        .await
        .expect("router must be ready");
    let response = <Router as Service<Request>>::call(&mut app, req)
        .await
        .expect("router must serve the request");

    assert_eq!(response.status(), StatusCode::OK);
    let events = store.events();
    assert_eq!(events.len(), 1, "expected one event, got {events:?}");
    let event: Value = serde_json::from_str(&events[0]).expect("event must be valid JSON");
    assert_eq!(event["event"].as_str(), Some("http.request"));
    assert_eq!(event["service"].as_str(), Some("catalog"));
    assert_eq!(event["attrs"]["status_code"].as_u64(), Some(200));
}
