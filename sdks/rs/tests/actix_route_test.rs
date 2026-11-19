#![cfg(feature = "actix")]

use actix_web::{
    test::{call_service, init_service, TestRequest},
    web, App, HttpResponse,
};
use loza::middleware::actix::LozaMiddleware;

#[actix_web::test]
async fn actix_middleware_keeps_route_unset_instead_of_aliasing_path() {
    let (logger, store) = loza::testkit();
    let middleware_logger = logger.clone();

    let app = init_service(
        App::new()
            .route(
                "/users/{id}",
                web::get().to(|| async { HttpResponse::Ok().finish() }),
            )
            .wrap(LozaMiddleware::new(middleware_logger, "web")),
    )
    .await;

    let req = TestRequest::get().uri("/users/123").to_request();
    let res = call_service(&app, req).await;
    assert_eq!(res.status(), 200);

    let _ = logger.flush();
    let events = store.events();
    assert_eq!(events.len(), 1, "expected exactly one emitted event");
    let event: serde_json::Value =
        serde_json::from_str(&events[0]).expect("event should be valid JSON");

    // The literal path is retained, but `route` must not be populated with the
    // same concrete value: there is no matched template available here, and
    // aliasing it would make route-based grouping/sampling unbounded.
    assert_eq!(event["path"].as_str(), Some("/users/123"));
    assert!(
        event.get("route").is_none(),
        "route must not alias the raw request path: {event}"
    );
}
