use loza::Config;
use std::sync::{Mutex, MutexGuard, OnceLock};

#[test]
fn env_overrides_file_defaults() {
    let _lock = lock_env();
    let _version = EnvRestore::set("LOZA_SERVICE_VERSION", "9.9.9");
    let _environment = EnvRestore::set("LOZA_ENVIRONMENT", "staging");
    let _region = EnvRestore::set("LOZA_REGION", "ap-south-1");
    let _endpoint = EnvRestore::set("LOZA_COLLECTOR_ENDPOINT", "http://collector.example/events");
    let _async = EnvRestore::set("LOZA_ASYNC_ENABLED", "true");

    let cfg = Config::test("checkout");

    assert_eq!(cfg.version, "9.9.9");
    assert_eq!(cfg.environment, "test");
    assert_eq!(cfg.region, "ap-south-1");
    assert_eq!(cfg.collector_endpoint, "http://collector.example/events");
    assert!(!cfg.async_enabled);
}

#[test]
fn plaintext_basic_auth_is_allowed_for_local_dsn_only() {
    let cfg = Config::test("checkout")
        .with_dsn("loza://localhost/project?tls=false")
        .with_basic_auth("dsn-user", "dsn-secret");
    assert!(
        cfg.validate().is_ok(),
        "local plaintext DSNs should remain usable"
    );
}

#[test]
fn level_validation_is_case_insensitive_and_accepts_full_set() {
    for level in [
        "trace", "debug", "info", "notice", "warn", "error", "fatal", "TRACE", "Info", "WaRn",
        "Notice",
    ] {
        let mut cfg = Config::test("checkout");
        cfg.level = level.to_string();
        assert!(
            cfg.validate().is_ok(),
            "level {level:?} should be accepted after case normalization"
        );
    }

    let mut cfg = Config::test("checkout");
    cfg.level = "verbose".to_string();
    assert!(
        cfg.validate().is_err(),
        "genuinely unsupported levels must still be rejected"
    );
}

#[test]
fn remote_plaintext_basic_auth_is_rejected() {
    let cfg = Config::test("checkout")
        .with_dsn("loza://dsn-user:dsn-secret@collector.example/project?tls=false");
    assert!(
        cfg.validate().is_err(),
        "remote plaintext Basic-auth DSNs must be rejected"
    );
}

#[test]
fn credentialed_dsn_configures_scoped_endpoint_and_preserves_api_key_precedence() {
    let capability = "lz_pub_6DJvd3D0izOaQx3n5BhKqN";
    let logger = loza::New(
        Config::production("checkout")
            .with_dsn(format!(
                "loza://{capability}:@collector.example/public-collector?env=prod"
            ))
            .with_api_key("api-key"),
    );

    assert_eq!(
        logger.config().collector_endpoint,
        "https://collector.example:443"
    );
    assert_eq!(logger.config().collector_name, "public-collector");
    match logger.config().sinks.first() {
        Some(loza::SinkConfig::HttpBatch {
            endpoint,
            api_key,
            basic_username,
            basic_password,
            ..
        }) => {
            assert_eq!(
                endpoint,
                "https://collector.example:443/collectors/public-collector/events"
            );
            assert_eq!(api_key.as_deref(), Some("api-key"));
            assert_eq!(basic_username.as_deref(), Some(capability));
            assert_eq!(basic_password.as_deref(), Some(""));
        }
        sink => panic!("expected scoped HTTP batch sink, got {sink:?}"),
    }
}

fn http_batch_sink(
    endpoint: &str,
    api_key: Option<&str>,
    basic: Option<(&str, &str)>,
    insecure: bool,
) -> loza::SinkConfig {
    loza::SinkConfig::HttpBatch {
        endpoint: endpoint.to_string(),
        api_key: api_key.map(str::to_string),
        basic_username: basic.map(|(user, _)| user.to_string()),
        basic_password: basic.map(|(_, password)| password.to_string()),
        insecure,
        timeout_ms: 2_000,
        max_batch_bytes: 256 * 1024,
        max_retries: 3,
        enable_compression: true,
        ndjson: false,
    }
}

#[test]
fn remote_plaintext_basic_auth_sink_is_rejected_even_when_insecure() {
    let cfg = Config::test("checkout").with_sink(http_batch_sink(
        "http://collector.example.com/events",
        None,
        Some(("svc", "secret")),
        true,
    ));
    assert!(
        cfg.validate().is_err(),
        "credentialed remote HTTP sinks must be rejected regardless of insecure"
    );
}

#[test]
fn credentialed_scheme_less_sink_is_rejected() {
    for endpoint in [
        "//collector.example.com/events",
        "collector.example.com:80/events",
    ] {
        let cfg = Config::test("checkout").with_sink(http_batch_sink(
            endpoint,
            None,
            Some(("svc", "secret")),
            false,
        ));
        assert!(
            cfg.validate().is_err(),
            "credentialed scheme-less sink endpoint {endpoint:?} must be rejected"
        );
    }
}

#[test]
fn api_key_over_remote_http_sink_is_rejected() {
    let cfg = Config::test("checkout").with_sink(http_batch_sink(
        "http://collector.example.com/events",
        Some("lz_pub_key"),
        None,
        false,
    ));
    assert!(
        cfg.validate().is_err(),
        "API keys must not be sent over remote plaintext HTTP"
    );
}

#[test]
fn https_and_explicit_local_insecure_credentialed_sinks_are_allowed() {
    let https = Config::test("checkout").with_sink(http_batch_sink(
        "https://collector.example.com/events",
        None,
        Some(("svc", "secret")),
        false,
    ));
    assert!(https.validate().is_ok(), "HTTPS sinks should validate");

    let local = Config::test("checkout").with_sink(http_batch_sink(
        "http://127.0.0.1:9308/events",
        None,
        Some(("svc", "secret")),
        true,
    ));
    assert!(
        local.validate().is_ok(),
        "explicit insecure local sinks should remain usable"
    );
}

/// Restores an environment variable to its prior value when dropped.
struct EnvRestore {
    key: &'static str,
    previous: Option<std::ffi::OsString>,
}

impl EnvRestore {
    fn set(key: &'static str, value: &str) -> Self {
        let previous = std::env::var_os(key);
        std::env::set_var(key, value);
        Self { key, previous }
    }
}

impl Drop for EnvRestore {
    fn drop(&mut self) {
        match &self.previous {
            Some(value) => std::env::set_var(self.key, value),
            None => std::env::remove_var(self.key),
        }
    }
}

/// Acquire the env-test mutex, recovering it if an earlier panic poisoned it.
fn lock_env() -> MutexGuard<'static, ()> {
    env_lock()
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

fn env_lock() -> &'static Mutex<()> {
    static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
    LOCK.get_or_init(|| Mutex::new(()))
}
