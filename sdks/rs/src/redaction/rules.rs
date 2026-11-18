use serde_json::Value;

use crate::config::RedactorConfig;

use super::keys::{hash_input, matches_key, sha256_hex, DEFAULT_KEYS};

pub fn redact(mut value: Value, redactor: &RedactorConfig) -> Value {
    match redactor {
        RedactorConfig::Default => redact_keys(&mut value, DEFAULT_KEYS, RedactionMode::Replace),
        RedactorConfig::Keys(keys) => {
            let refs: Vec<_> = keys.iter().map(String::as_str).collect();
            redact_keys(&mut value, &refs, RedactionMode::Replace)
        }
        RedactorConfig::HashKeys(keys) => {
            let refs: Vec<_> = keys.iter().map(String::as_str).collect();
            redact_keys(&mut value, &refs, RedactionMode::Hash)
        }
        RedactorConfig::DropKeys(keys) => {
            let refs: Vec<_> = keys.iter().map(String::as_str).collect();
            redact_keys(&mut value, &refs, RedactionMode::Drop)
        }
        RedactorConfig::MaskKeys(keys) => {
            let refs: Vec<_> = keys.iter().map(String::as_str).collect();
            redact_keys(&mut value, &refs, RedactionMode::Mask)
        }
        RedactorConfig::Patterns(patterns) => {
            redact_patterns(&mut value, patterns);
            value
        }
        RedactorConfig::Compose(redactors) => redactors.iter().fold(value, redact),
        RedactorConfig::AllowKeys(keys) => {
            let refs: Vec<_> = keys.iter().map(String::as_str).collect();
            redact_allowlist(&mut value, &refs)
        }
        RedactorConfig::None => value,
    }
}

pub enum RedactionMode {
    Replace,
    Hash,
    Drop,
    Mask,
}

fn redact_keys(value: &mut Value, keys: &[&str], mode: RedactionMode) -> Value {
    walk(value, keys, &mode);
    value.clone()
}

// Allowlist semantics: keep every scalar field whose key is in `keys` and
// redact every other scalar field. Containers are always traversed so that
// allowed fields nested inside them are preserved.
fn redact_allowlist(value: &mut Value, keys: &[&str]) -> Value {
    walk_allowlist(value, keys);
    value.clone()
}

fn walk_allowlist(value: &mut Value, keys: &[&str]) {
    match value {
        Value::Object(map) => {
            let names: Vec<_> = map.keys().cloned().collect();
            for key in names {
                let is_container = matches!(
                    map.get(&key),
                    Some(Value::Object(_)) | Some(Value::Array(_))
                );
                if !is_container && !matches_key(&key, keys) {
                    map.insert(key, Value::String("[REDACTED]".to_string()));
                } else if let Some(child) = map.get_mut(&key) {
                    walk_allowlist(child, keys);
                }
            }
        }
        Value::Array(values) => {
            for child in values {
                walk_allowlist(child, keys);
            }
        }
        _ => {}
    }
}

fn redact_patterns(value: &mut Value, patterns: &[String]) {
    // An invalid redaction pattern must never be silently dropped: doing so
    // would emit data the caller believed was being scrubbed. Fail loudly with
    // a message that identifies the offending pattern and the reason it is
    // invalid, so misconfigured PII/redaction rules are caught immediately.
    let compiled: Vec<regex::Regex> = patterns
        .iter()
        .map(|p| {
            regex::Regex::new(p)
                .unwrap_or_else(|err| panic!("invalid redaction pattern {p:?}: {err}"))
        })
        .collect();
    walk_patterns(value, &compiled);
}

fn walk_patterns(value: &mut Value, patterns: &[regex::Regex]) {
    match value {
        Value::Object(map) => {
            for (_key, val) in map.iter_mut() {
                if let Value::String(s) = val {
                    for pat in patterns {
                        if pat.is_match(s) {
                            *val = Value::String("[REDACTED]".to_string());
                            break;
                        }
                    }
                } else {
                    walk_patterns(val, patterns);
                }
            }
        }
        Value::Array(values) => {
            for child in values {
                walk_patterns(child, patterns);
            }
        }
        _ => {}
    }
}

fn walk(value: &mut Value, keys: &[&str], mode: &RedactionMode) {
    match value {
        Value::Object(map) => {
            let names: Vec<_> = map.keys().cloned().collect();
            for key in names {
                if matches_key(&key, keys) {
                    match mode {
                        RedactionMode::Replace => {
                            map.insert(key, Value::String("[REDACTED]".to_string()));
                        }
                        RedactionMode::Hash => {
                            let hashed = sha256_hex(&hash_input(&map[&key]));
                            map.insert(key, Value::String(hashed));
                        }
                        RedactionMode::Drop => {
                            map.remove(&key);
                        }
                        RedactionMode::Mask => {
                            let text = hash_input(&map[&key]);
                            let masked = if text.len() <= 4 {
                                "****".to_string()
                            } else {
                                let chars: Vec<char> = text.chars().collect();
                                let prefix: String = chars[..2].iter().collect();
                                let suffix: String = chars[chars.len() - 2..].iter().collect();
                                let stars = "*".repeat(chars.len() - 4);
                                format!("{prefix}{stars}{suffix}")
                            };
                            map.insert(key, Value::String(masked));
                        }
                    }
                } else if let Some(child) = map.get_mut(&key) {
                    walk(child, keys, mode);
                }
            }
        }
        Value::Array(values) => {
            for child in values {
                walk(child, keys, mode);
            }
        }
        _ => {}
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn allow_keys_redacts_every_field_not_in_the_allowlist() {
        let value = json!({
            "user_id": "u1",
            "attrs": {
                "message": "hello",
                "token": "abc123",
                "nested": { "city": "X", "zip": "12345" }
            }
        });

        let redacted = redact(
            value,
            &RedactorConfig::AllowKeys(vec!["message".into(), "city".into()]),
        );

        assert_eq!(redacted["attrs"]["message"], "hello");
        assert_eq!(redacted["attrs"]["token"], "[REDACTED]");
        assert_eq!(redacted["attrs"]["nested"]["city"], "X");
        assert_eq!(redacted["attrs"]["nested"]["zip"], "[REDACTED]");
        assert_eq!(redacted["user_id"], "[REDACTED]");
    }

    #[test]
    fn allow_keys_redacts_everything_when_no_allowlist_is_given() {
        let value = json!({ "message": "hello", "token": "abc123" });

        let redacted = redact(value, &RedactorConfig::AllowKeys(vec![]));

        assert_eq!(redacted["message"], "[REDACTED]");
        assert_eq!(redacted["token"], "[REDACTED]");
    }

    #[test]
    fn valid_patterns_still_redact_matching_strings() {
        let value = json!({
            "message": "contact me at secret@example.com",
            "nested": { "detail": "another test@example.org" }
        });
        let redactor = RedactorConfig::Patterns(vec![r"[\w.]+@[\w.]+".to_string()]);

        let redacted = redact(value, &redactor);

        assert_eq!(redacted["message"], "[REDACTED]");
        assert_eq!(redacted["nested"]["detail"], "[REDACTED]");
    }

    #[test]
    #[should_panic(expected = "invalid redaction pattern")]
    fn invalid_pattern_is_not_silently_dropped() {
        // Previously the invalid pattern was discarded via `.filter_map(...).ok()`
        // and the value was returned untouched, leaking data the caller expected
        // to be redacted. It must now fail loudly instead.
        let value = json!({ "message": "sensitive data" });
        let redactor = RedactorConfig::Patterns(vec!["[unclosed".to_string()]);

        let _ = redact(value, &redactor);
    }

    #[test]
    #[should_panic(expected = "invalid redaction pattern")]
    fn invalid_pattern_is_not_silently_dropped_within_compose() {
        let value = json!({ "message": "sensitive data" });
        let redactor = RedactorConfig::Compose(vec![
            RedactorConfig::Default,
            RedactorConfig::Patterns(vec!["(".to_string()]),
        ]);

        let _ = redact(value, &redactor);
    }
}
