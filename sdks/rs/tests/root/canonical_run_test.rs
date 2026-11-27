#[test]
fn root_lifecycle_exports_emit() {
    let logger = loza::New(loza::Config::test("root"));
    let mut ctx = logger.start_event(loza::Params::new("root.run"));
    logger
        .finish(&mut ctx, "success")
        .expect("root lifecycle event should finish");
    assert!(logger
        .emit(&ctx)
        .unwrap()
        .contains("\"schema_version\":\"v1\""));
}
