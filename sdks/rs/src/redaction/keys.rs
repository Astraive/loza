// Safety-net keys for obviously sensitive fields. The collector owns the real
// PII policy (email, phone, SSN, IP, regex, tenant-specific rules, etc.).
pub const DEFAULT_KEYS: &[&str] = &[
    "password",
    "passwd",
    "pwd",
    "secret",
    "token",
    "access_token",
    "refresh_token",
    "api_key",
    "apikey",
    "auth",
    "authorization",
    "credential",
    "private_key",
    "client_secret",
];

fn segments(value: &str) -> Vec<&str> {
    value.split(|c| c == '.' || c == '_').collect()
}

/// Returns true when `needle` appears as a contiguous run of segments in
/// `haystack`. Both sides are split on `.` and `_`, so a sensitive key is
/// matched wherever it occurs in a dotted/underscored field name.
fn contains_segments(haystack: &[&str], needle: &[&str]) -> bool {
    !needle.is_empty()
        && haystack
            .windows(needle.len())
            .any(|window| window == needle)
}

pub fn matches_key(key: &str, keys: &[&str]) -> bool {
    let lowered = key.to_ascii_lowercase();
    let key_segments = segments(&lowered);
    keys.iter()
        .any(|wanted| contains_segments(&key_segments, &segments(wanted)))
}

#[cfg(test)]
mod tests {
    use super::{matches_key, DEFAULT_KEYS};

    #[test]
    fn matches_sensitive_key_with_suffix() {
        // Regression: the matcher used to inspect only the last `.`/`_` segment,
        // so `hash`/`value` never matched `password`/`token` and these leaked.
        assert!(matches_key("password_hash", DEFAULT_KEYS));
        assert!(matches_key("token_value", DEFAULT_KEYS));
        assert!(matches_key("user.secret_suffix", DEFAULT_KEYS));
        assert!(matches_key("api_key_id", DEFAULT_KEYS));
    }

    #[test]
    fn matches_sensitive_key_in_any_position() {
        assert!(matches_key("db.password.reset", DEFAULT_KEYS));
        assert!(matches_key("req.auth_token.value", DEFAULT_KEYS));
        // Composite keys must survive a prefix/suffix of their own.
        assert!(matches_key("my_private_key_backup", DEFAULT_KEYS));
        assert!(matches_key("client_secret_ciphertext", DEFAULT_KEYS));
    }

    #[test]
    fn preserves_exact_and_last_segment_matching() {
        assert!(matches_key("password", DEFAULT_KEYS));
        assert!(matches_key("user.password", DEFAULT_KEYS));
        assert!(matches_key("user_password", DEFAULT_KEYS));
        assert!(matches_key("access_token", DEFAULT_KEYS));
    }

    #[test]
    fn does_not_match_unrelated_keys() {
        assert!(!matches_key("username", DEFAULT_KEYS));
        assert!(!matches_key("timing_ms", DEFAULT_KEYS));
        assert!(!matches_key("request.id", DEFAULT_KEYS));
        // Segment matching must not devolve into a loose substring match.
        assert!(!matches_key("tokenizer_mode", DEFAULT_KEYS));
    }
}

pub fn hash_input(value: &serde_json::Value) -> String {
    match value {
        serde_json::Value::String(text) => text.clone(),
        _ => value.to_string(),
    }
}
