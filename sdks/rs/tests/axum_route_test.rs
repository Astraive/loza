#![cfg(feature = "axum")]

use axum::body::Body;
use axum::extract::Request;
use axum::http::StatusCode;
use axum::middleware;
use axum::response::IntoResponse;
use axum::routing::get;
use axum::Router;
use loza::middleware::axum::loza_middleware;
use std::sync::Arc;
use tower::Service;

async fn ok() -> impl IntoResponse {
    StatusCode::OK
}

#[tokio::test]
async fn axum_middleware_uses_matched_route_template_not_raw_path() {
    let (logger, store) = loza::testkit();
    let logger = Arc::new(logger);
    let middleware_logger = Arc::clone(&logger);

    let mut app = Router::new()
        .route("/users/:id", get(ok))
        .layer(middleware::from_fn(move |req, next| {
            let logger = Arc::clone(&middleware_logger);
            loza_middleware(logger, "web".to_string(), req, next)
        }));

    let req = Request::builder()
        .method("GET")
        .uri("/users/123")
        .body(Body::empty())
        .expect("build request");

    let res = app.call(req).await.expect("router call");
    assert_eq!(res.status(), StatusCode::OK);

    let _ = logger.flush();
    let events = store.events();
    assert_eq!(events.len(), 1, "expected exactly one emitted event");
    let event: serde_json::Value =
        serde_json::from_str(&events[0]).expect("event should be valid JSON");

    // The literal path is preserved separately; route must be the template so
    // route-based grouping/sampling does not get unbounded cardinality.
    assert_eq!(event["path"].as_str(), Some("/users/123"));
    assert_eq!(
        event["route"].as_str(),
        Some("/users/:id"),
        "route must be the matched route template, not the raw URI path"
    );
}

#[tokio::test]
async fn axum_middleware_keeps_literal_path_when_no_route_matched() {
    let (logger, store) = loza::testkit();
    let logger = Arc::new(logger);
    let middleware_logger = Arc::clone(&logger);

    let mut app = Router::new()
        .route("/users/:id", get(ok))
        .layer(middleware::from_fn(move |req, next| {
            let logger = Arc::clone(&middleware_logger);
            loza_middleware(logger, "web".to_string(), req, next)
        }));

    let req = Request::builder()
        .method("GET")
        .uri("/not-a-route/456")
        .body(Body::empty())
        .expect("build request");

    let res = app.call(req).await.expect("router call");
    assert_eq!(res.status(), StatusCode::NOT_FOUND);

    let _ = logger.flush();
    let events = store.events();
    assert_eq!(events.len(), 1, "expected exactly one emitted event");
    let event: serde_json::Value =
        serde_json::from_str(&events[0]).expect("event should be valid JSON");

    assert_eq!(event["path"].as_str(), Some("/not-a-route/456"));
    assert_eq!(
        event["route"].as_str(),
        Some("/not-a-route/456"),
        "without a matched template, route falls back to the literal path"
    );
}
