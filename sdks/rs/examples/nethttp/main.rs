use loza::{
    middleware::tower::{capture_request, MiddlewareResult},
    Config, New,
};

/// Convert the result of a request capture into the payload to print.
///
/// The error is deliberately kept in the `Result` so that a failed event emit
/// is surfaced to the caller instead of being replaced with an empty string.
fn encoded_from(
    result: Result<MiddlewareResult, serde_json::Error>,
) -> Result<String, serde_json::Error> {
    result.map(|result| result.encoded)
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let logger = New(Config::test("web"));
    let encoded = encoded_from(capture_request(&logger, "GET", "/ready", 200))?;
    println!("{encoded}");
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use loza::SinkConfig;

    #[test]
    fn successful_capture_returns_non_empty_encoded_output() {
        let logger = New(Config::test("web"));
        let encoded = encoded_from(capture_request(&logger, "GET", "/ready", 200))
            .expect("a successful emit must not fail");
        assert!(
            !encoded.is_empty(),
            "a successful capture must not print a blank line"
        );
    }

    #[test]
    fn emit_failure_is_surfaced_instead_of_becoming_an_empty_string() {
        let mut config = Config::test("web");
        // A file sink whose parent directory does not exist makes `emit` fail,
        // which `capture_request` reports as `serde_json::Error`.
        config.sinks = vec![SinkConfig::File(
            std::env::temp_dir()
                .join("loza-mass-0752-missing-parent")
                .join("events.jsonl")
                .to_string_lossy()
                .into_owned(),
        )];
        let logger = New(config);
        let result = encoded_from(capture_request(&logger, "GET", "/ready", 200));
        assert!(
            result.is_err(),
            "a failed emit must propagate as an error, not be swallowed into an empty string"
        );
    }
}
