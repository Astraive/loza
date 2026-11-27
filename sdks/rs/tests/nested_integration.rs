// Keep the historical nested integration tests discoverable by Cargo.
#[path = "root/canonical_run_test.rs"]
mod canonical_run_test;
#[path = "integration/middleware_test.rs"]
mod middleware_test;
