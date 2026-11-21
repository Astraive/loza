use loza::{Config, Hash, HashKeys, New, Params};
use serde_json::Value;

const SECRET: &str = "loza-regression-test-secret";

fn hmac_sha256_hex(key: &str, value: &str) -> String {
    use sha2::{Digest, Sha256};
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

fn plain_sha256_hex(value: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(value.as_bytes());
    format!("{:x}", hasher.finalize())
}

fn hash_keys_payload(value: &str) -> Value {
    std::env::set_var("LOZA_HASH_SECRET", SECRET);
    let logger = New(Config::test("hashing").with_redactor(HashKeys(&["password"])));
    let mut ctx = logger.start_event(Params::new("hashing.test"));
    logger.append(&mut ctx, loza::String("password", value));
    let _ = logger.finish(&mut ctx, "success");
    serde_json::from_str(&logger.emit(&ctx).unwrap()).unwrap()
}

#[test]
fn hash_keys_uses_keyed_hmac_not_plain_sha256() {
    let value = "1234";
    let payload = hash_keys_payload(value);
    let hashed = payload["attrs"]["password"].as_str().unwrap();

    // The original defect: a deterministic SHA-256 digest of the low-entropy
    // value, trivially reversed with a rainbow table.
    assert_ne!(hashed, plain_sha256_hex(value));
    // With a configured secret, the digest is exactly HMAC-SHA256(secret, value).
    assert_eq!(hashed, hmac_sha256_hex(SECRET, value));
}

#[test]
fn hash_value_attr_uses_keyed_hmac_not_plain_sha256() {
    std::env::set_var("LOZA_HASH_SECRET", SECRET);
    let value = "4567";
    let logger = New(Config::test("hashing"));
    let mut ctx = logger.start_event(Params::new("hashing.attr"));
    logger.append(&mut ctx, Hash("pin", value));
    let _ = logger.finish(&mut ctx, "success");

    let payload: Value = serde_json::from_str(&logger.emit(&ctx).unwrap()).unwrap();
    let hashed = payload["attrs"]["pin"].as_str().unwrap();

    assert_ne!(hashed, plain_sha256_hex(value));
    assert_eq!(hashed, hmac_sha256_hex(SECRET, value));
}
