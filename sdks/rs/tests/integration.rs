// Integration wrapper for Rust SDK conformance
// Re-exports all e2e tests to match conformance runner expectations
#[path = "root/canonical_run_test.rs"]
mod canonical_run;
mod e2e;

#[path = "integration/middleware_test.rs"]
mod middleware;

// The conformance runner will find this test module via `cargo test --test integration`
