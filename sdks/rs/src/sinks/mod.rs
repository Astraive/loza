pub mod httpbatch;

pub use httpbatch::*;

use crate::config::MemorySinkStore;
use crate::sink;
use crate::SinkConfig;
use std::io;

pub fn stdout() -> SinkConfig {
    SinkConfig::Stdout
}

pub fn stderr() -> SinkConfig {
    SinkConfig::Stderr
}

pub fn memory() -> SinkConfig {
    SinkConfig::Memory(MemorySinkStore::new())
}

pub fn noop() -> SinkConfig {
    SinkConfig::Noop
}

pub fn multi_sink(sinks: &[SinkConfig]) -> Vec<SinkConfig> {
    sinks.to_vec()
}

pub fn otlp_sink(endpoint: impl Into<String>) -> SinkConfig {
    SinkConfig::HttpBatch {
        endpoint: endpoint.into(),
        api_key: None,
        basic_username: None,
        basic_password: None,
        insecure: false,
        timeout_ms: 2_000,
        max_batch_bytes: 256 * 1024,
        max_retries: 3,
        enable_compression: true,
        ndjson: false,
    }
}

/// Flush `sink_cfg`, propagating any I/O error to the caller.
///
/// # Migration note
/// This function previously returned `()` and silently discarded the result
/// of `flush_sink`. It now returns `io::Result<()>` so failed flushes (for
/// example a broken pipe on stdout/stderr) are observable. Callers that want
/// the old fire-and-forget behaviour can use `let _ = drain(cfg);` or
/// `drain(cfg).ok();`.
pub fn drain(sink_cfg: &SinkConfig) -> io::Result<()> {
    drain_with(sink_cfg, sink::flush_sink)
}

/// Flush `sink_cfg` through `flush`, returning the result unchanged.
///
/// This is the single propagation point exercised by the regression tests.
fn drain_with(
    sink_cfg: &SinkConfig,
    flush: impl FnOnce(&SinkConfig) -> io::Result<()>,
) -> io::Result<()> {
    flush(sink_cfg)
}

pub fn pause(sink_cfg: &SinkConfig) {
    sink::pause_sink(sink_cfg);
}

pub fn resume(sink_cfg: &SinkConfig) {
    sink::resume_sink(sink_cfg);
}

pub fn queue_size(sink_cfg: &SinkConfig) -> usize {
    sink::sink_queue_size(sink_cfg)
}

pub fn health(sink_cfg: &SinkConfig) -> bool {
    sink::sink_health(sink_cfg)
}
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn drain_propagates_flush_failure() {
        let sink = stdout();
        let result = drain_with(&sink, |_| Err(io::Error::other("flush failed")));
        assert_eq!(result.unwrap_err().to_string(), "flush failed");
    }

    #[test]
    fn drain_returns_ok_for_non_io_sinks() {
        assert!(drain(&memory()).is_ok());
        assert!(drain(&noop()).is_ok());
    }
}
