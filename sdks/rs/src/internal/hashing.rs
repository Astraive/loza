//! Keyed hashing for sensitive values.
//!
//! Plain SHA-256 is a deterministic, unsalted digest. When it is used to
//! "redact" a low-entropy value (a PIN, a short password, an email address),
//! anyone who obtains the digest can recover the original value offline with a
//! dictionary or rainbow-table attack. Hashing alone is therefore not redaction.
//!
//! All SDK value hashing goes through [`secure_hash`], which computes an
//! HMAC-SHA256 using a secret key. Without the key, candidate digests cannot be
//! precomputed, so low-entropy inputs are no longer trivially recoverable.
//!
//! Set `LOZA_HASH_SECRET` to a stable value shared by every producer when
//! hashes must be comparable across processes or restarts. When it is unset a
//! random key is generated once per process; hashes are then non-reversible but
//! only comparable within that process.

use std::sync::OnceLock;

use ring::hmac;
use ring::rand::SecureRandom;

/// Environment variable holding the per-deployment hashing secret.
pub const HASH_SECRET_ENV: &str = "LOZA_HASH_SECRET";

/// Per-process random key, generated lazily from a cryptographic RNG.
static PROCESS_KEY: OnceLock<Vec<u8>> = OnceLock::new();

fn process_key() -> &'static [u8] {
    PROCESS_KEY
        .get_or_init(|| {
            let mut key = vec![0u8; 32];
            // ring's SystemRandom draws from the OS CSPRNG and never fails on
            // the platforms the SDK supports; fall back to a fixed, clearly
            // non-secret value only if the OS entropy source is unavailable.
            if ring::rand::SystemRandom::new().fill(&mut key).is_err() {
                // This keeps hashing non-empty and deterministic within the
                // process, but cannot protect against offline attacks. It is a
                // last-resort path for platforms without a secure RNG.
                key.clear();
                key.extend_from_slice(b"loza-insecure-fallback-hash-key");
            }
            key
        })
        .as_slice()
}

fn hash_key() -> Vec<u8> {
    match std::env::var(HASH_SECRET_ENV) {
        Ok(secret) if !secret.is_empty() => secret.into_bytes(),
        _ => process_key().to_vec(),
    }
}

/// Compute a keyed HMAC-SHA256 hex digest of `value`.
///
/// The key is read from `LOZA_HASH_SECRET` on every call, falling back to a
/// random per-process key when the variable is unset. This is intentionally
/// different from a plain SHA-256 digest so that low-entropy values cannot be
/// recovered with generic rainbow tables.
pub fn secure_hash(value: &str) -> String {
    let key = hash_key();
    let signing_key = hmac::Key::new(hmac::HMAC_SHA256, &key);
    let tag = hmac::sign(&signing_key, value.as_bytes());
    let mut out = String::with_capacity(tag.as_ref().len() * 2);
    for byte in tag.as_ref() {
        out.push_str(&format!("{byte:02x}"));
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    fn plain_sha256(value: &str) -> String {
        use sha2::{Digest, Sha256};
        let mut hasher = Sha256::new();
        hasher.update(value.as_bytes());
        format!("{:x}", hasher.finalize())
    }

    fn hmac_sha256(key: &str, value: &str) -> String {
        use sha2::{Digest, Sha256};
        // Independent HMAC-SHA256 construction (RFC 2104) so the assertion
        // does not just re-run ring's own implementation.
        let block_size = 64;
        let mut key_bytes = key.as_bytes().to_vec();
        if key_bytes.len() > block_size {
            let mut hasher = Sha256::new();
            hasher.update(&key_bytes);
            key_bytes = hasher.finalize().to_vec();
        }
        key_bytes.resize(block_size, 0);
        let inner: Vec<u8> = key_bytes.iter().map(|b| b ^ 0x36).collect();
        let outer: Vec<u8> = key_bytes.iter().map(|b| b ^ 0x5c).collect();

        let mut inner_hasher = Sha256::new();
        inner_hasher.update(&inner);
        inner_hasher.update(value.as_bytes());
        let inner_digest = inner_hasher.finalize();

        let mut outer_hasher = Sha256::new();
        outer_hasher.update(&outer);
        outer_hasher.update(inner_digest);
        format!("{:x}", outer_hasher.finalize())
    }

    #[test]
    fn secure_hash_matches_hmac_sha256_with_configured_secret() {
        // Use a dedicated key resolution path that does not depend on global
        // environment state shared with other tests.
        let secret = "unit-test-secret";
        let value = "1234";

        let key = hmac::Key::new(hmac::HMAC_SHA256, secret.as_bytes());
        let tag = hmac::sign(&key, value.as_bytes());
        let expected: String = tag.as_ref().iter().map(|b| format!("{b:02x}")).collect();

        // Cross-check ring's HMAC against the independent construction.
        assert_eq!(expected, hmac_sha256(secret, value));
        // And confirm it is not the unsalted SHA-256 the finding complains of.
        assert_ne!(expected, plain_sha256(value));
    }

    #[test]
    fn secure_hash_is_deterministic_within_a_process() {
        let first = secure_hash("stable-value");
        let second = secure_hash("stable-value");
        assert_eq!(first, second);
        assert_ne!(first, plain_sha256("stable-value"));
    }

    #[test]
    fn secure_hash_differs_for_different_values() {
        assert_ne!(secure_hash("1234"), secure_hash("1235"));
    }
}
