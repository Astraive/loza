use crate::config::MemorySinkStore;
use crate::{Config, EventContext, Logger, SinkConfig};

pub fn test_logger(service: &str) -> Logger {
    Logger::new(Config::test(service).with_sink(SinkConfig::Memory(MemorySinkStore::new())))
}

/// Spec-aligned alias for test_logger.
pub fn testkit(service: &str) -> (Logger, MemorySinkStore) {
    let store = MemorySinkStore::new();
    let logger = Logger::new(Config::test(service).with_sink(SinkConfig::Memory(store.clone())));
    (logger, store)
}

pub fn assert_contains(encoded: &str, needle: &str) {
    assert!(encoded.contains(needle), "event did not contain {needle}");
}

/// Run a closure with a temporary memory sink and return captured event JSON strings.
pub fn capture(f: impl FnOnce(&Logger)) -> Vec<String> {
    let store = MemorySinkStore::new();
    let logger = Logger::new(Config::test("capture").with_sink(SinkConfig::Memory(store.clone())));
    capture_with(&logger, f)
}

/// Flush `logger` and collect captured events from its memory sinks.
///
/// A failed flush must surface as a test failure: silently discarding the
/// `Result` would let callers assert against incomplete or empty events.
fn capture_with(logger: &Logger, f: impl FnOnce(&Logger)) -> Vec<String> {
    f(logger);
    logger.flush().expect("capture: flush failed");
    let mut events = Vec::new();
    for sink in &logger.config().sinks {
        if let SinkConfig::Memory(store) = sink {
            events.extend(store.events());
        }
    }
    events
}

/// Assert that a decoded event JSON contains the expected key-value pair.
pub fn assert_event(encoded: &str, key: &str, expected: &str) {
    let parsed: serde_json::Value =
        serde_json::from_str(encoded).expect("assert_event: failed to parse event JSON");
    let actual = get_nested_value(&parsed, key);
    match actual {
        Some(val) => {
            let val_str = match val {
                serde_json::Value::String(s) => s.clone(),
                other => serde_json::to_string(&other).unwrap_or_default(),
            };
            assert_eq!(
                val_str, expected,
                "assert_event: key \"{key}\" expected \"{expected}\", got \"{val_str}\""
            );
        }
        None => panic!("assert_event: key \"{key}\" not found in event"),
    }
}

/// Assert that a field value is `"[REDACTED]"`.
pub fn assert_redacted(encoded: &str, key: &str) {
    assert_event(encoded, key, "[REDACTED]");
}

/// Assert that a checkpoint with the given name exists.
pub fn assert_has_checkpoint(encoded: &str, name: &str) {
    let parsed: serde_json::Value =
        serde_json::from_str(encoded).expect("assert_has_checkpoint: failed to parse event JSON");
    if let Some(checkpoints) = parsed.get("checkpoints").and_then(|v| v.as_array()) {
        let found = checkpoints.iter().any(|cp| {
            cp.get("name")
                .and_then(|n| n.as_str())
                .map(|n| n == name)
                .unwrap_or(false)
        });
        assert!(
            found,
            "assert_has_checkpoint: checkpoint \"{name}\" not found"
        );
    } else {
        panic!("assert_has_checkpoint: no checkpoints array in event");
    }
}

pub fn expect_event(logger: &Logger, name: &str, f: impl FnOnce(&EventContext)) {
    for sink in &logger.config().sinks {
        if let SinkConfig::Memory(store) = sink {
            for encoded in store.events() {
                if let Ok(val) = serde_json::from_str::<serde_json::Value>(&encoded) {
                    if val.get("event").and_then(|v| v.as_str()) == Some(name) {
                        let ctx: EventContext = serde_json::from_value(val)
                            .expect("expect_event: failed to deserialize event context");
                        f(&ctx);
                        return;
                    }
                }
            }
        }
    }
    panic!("expect_event: no event named \"{name}\" found");
}

pub fn expect_attr(event: &EventContext, key: &str, expected: &serde_json::Value) -> bool {
    event.attrs.get(key) == Some(expected)
}

pub fn snapshot_event(event: &EventContext) -> String {
    serde_json::to_string(event).unwrap_or_default()
}

pub fn mock_sink() -> SinkConfig {
    SinkConfig::Memory(MemorySinkStore::new())
}

pub fn fake_clock(unix_ms: u128) {
    crate::internal::clock::freeze_at(unix_ms);
}

pub fn set_id_generator(f: fn() -> String) {
    crate::internal::core::uuidv7::set_id_generator(Box::new(f));
}

/// Reset all global mutable state: global logger, clock, and ID generator.
pub fn reset_for_test() {
    crate::set_global_logger(Logger::new(Config::dev("loza")));
    crate::internal::clock::unfreeze();
    crate::internal::core::uuidv7::reset_id_generator();
}

/// Split a testkit path into segments.
///
/// Segments are separated by unescaped `.` characters. A dot can be matched
/// literally inside a key by escaping it as `\.`, and a literal backslash as
/// `\\`. For example, `attrs.a\.b` addresses the key `a.b` inside `attrs`.
fn split_path(path: &str) -> Vec<String> {
    let mut segments = Vec::new();
    let mut current = String::new();
    let mut chars = path.chars().peekable();
    while let Some(ch) = chars.next() {
        match ch {
            '\\' => match chars.next() {
                Some('.') => current.push('.'),
                Some('\\') => current.push('\\'),
                Some(other) => {
                    // Preserve unrecognised escapes verbatim rather than
                    // silently dropping the backslash.
                    current.push('\\');
                    current.push(other);
                }
                None => current.push('\\'),
            },
            '.' => segments.push(std::mem::take(&mut current)),
            other => current.push(other),
        }
    }
    segments.push(current);
    segments
}

/// Resolve a dotted path against a JSON value.
///
/// Plain segments address object keys. When the current value is an array, a
/// numeric segment is used as an index (e.g. `checkpoints.0.name`). Dots that
/// are part of a key must be escaped (`a\.b`); an unescaped numeric segment on
/// an object still addresses a key of that name.
fn get_nested_value<'a>(obj: &'a serde_json::Value, path: &str) -> Option<&'a serde_json::Value> {
    let mut current = obj;
    for segment in split_path(path) {
        current = match current {
            serde_json::Value::Array(items) => items.get(segment.parse::<usize>().ok()?)?,
            _ => current.get(segment.as_str())?,
        };
    }
    Some(current)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sample() -> serde_json::Value {
        serde_json::json!({
            "attrs": { "family": "testkit" },
            "checkpoints": [
                { "name": "first" },
                { "name": "second" }
            ],
            "dotted.key": "literal",
            "nested": { "a.b": { "c": "deep" } },
            "0": "numeric-key"
        })
    }

    #[test]
    fn get_nested_value_indexes_arrays() {
        let value = sample();
        assert_eq!(
            get_nested_value(&value, "checkpoints.0.name").and_then(|v| v.as_str()),
            Some("first")
        );
        assert_eq!(
            get_nested_value(&value, "checkpoints.1.name").and_then(|v| v.as_str()),
            Some("second")
        );
        assert!(get_nested_value(&value, "checkpoints.2.name").is_none());
        assert!(get_nested_value(&value, "checkpoints.name").is_none());
    }

    #[test]
    fn get_nested_value_resolves_escaped_dotted_keys() {
        let value = sample();
        assert_eq!(
            get_nested_value(&value, "dotted\\.key").and_then(|v| v.as_str()),
            Some("literal")
        );
        assert_eq!(
            get_nested_value(&value, "nested.a\\.b.c").and_then(|v| v.as_str()),
            Some("deep")
        );
    }

    #[test]
    fn get_nested_value_preserves_plain_object_paths() {
        let value = sample();
        assert_eq!(
            get_nested_value(&value, "attrs.family").and_then(|v| v.as_str()),
            Some("testkit")
        );
        // A numeric segment still addresses an object key of that name.
        assert_eq!(
            get_nested_value(&value, "0").and_then(|v| v.as_str()),
            Some("numeric-key")
        );
    }

    #[test]
    fn assert_event_supports_array_indices_and_escaped_keys() {
        let encoded = serde_json::to_string(&sample()).expect("encode sample");
        assert_event(&encoded, "checkpoints.1.name", "second");
        assert_event(&encoded, "dotted\\.key", "literal");
    }

    #[test]
    #[should_panic(expected = "capture: flush failed")]
    fn capture_surfaces_flush_failure() {
        let logger = Logger::new(
            Config::test("capture-flush-failure")
                .with_async(true)
                .with_sink(SinkConfig::Memory(MemorySinkStore::new())),
        );
        // Shutting down the async worker makes a subsequent flush fail
        // deterministically (sending the flush command finds no receiver).
        logger.shutdown().expect("shutdown should succeed");
        capture_with(&logger, |_| {});
    }

    #[test]
    fn capture_collects_events_after_flush() {
        reset_for_test();
        let events = capture(|logger| {
            let mut event = logger.start_event(crate::Params::new("capture.flush.ok"));
            logger.append(&mut event, crate::string("family", "testkit"));
            let _ = logger.finish(&mut event, "success");
            let _ = logger.emit(&event);
        });
        assert_eq!(events.len(), 1, "capture should return all flushed events");
    }
}
