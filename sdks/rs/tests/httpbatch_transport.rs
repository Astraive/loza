use flate2::read::GzDecoder;
use loza::{Config, New, Params};
use std::io::{Read, Write};
use std::net::TcpListener;
use std::sync::mpsc;
use std::sync::{Mutex, MutexGuard, OnceLock};
use std::thread;
use std::time::Duration;

#[test]
fn http_batch_sink_posts_gzipped_events_to_collector() {
    // The collector client reads LOZA_COLLECTOR_API_KEY /
    // LOZA_COLLECTOR_API_KEY_HEADER from the process-global environment at
    // emit time. Serialize with the key-from-environment test so its
    // transient set_var/remove_var cannot leak into this sink's request.
    let _guard = env_lock().lock().expect("env lock");
    let listener = TcpListener::bind("127.0.0.1:0").expect("bind test server");
    listener
        .set_nonblocking(false)
        .expect("configure blocking listener");
    let addr = listener.local_addr().expect("listener addr");
    let (tx, rx) = mpsc::channel();

    let handle = thread::spawn(move || {
        let (mut stream, _) = listener.accept().expect("accept request");
        stream
            .set_read_timeout(Some(Duration::from_secs(5)))
            .expect("set timeout");

        let mut raw = Vec::new();
        let mut headers_done = false;
        let mut content_length = 0usize;
        loop {
            let mut chunk = [0u8; 1024];
            let n = stream.read(&mut chunk).expect("read request");
            if n == 0 {
                break;
            }
            raw.extend_from_slice(&chunk[..n]);
            if !headers_done {
                if let Some(pos) = find_headers_end(&raw) {
                    headers_done = true;
                    let head = String::from_utf8_lossy(&raw[..pos]);
                    content_length = parse_content_length(&head);
                    let body_len = raw.len() - (pos + 4);
                    if body_len >= content_length {
                        break;
                    }
                }
            } else if let Some(pos) = find_headers_end(&raw) {
                let body_len = raw.len() - (pos + 4);
                if body_len >= content_length {
                    break;
                }
            }
        }

        let headers_end = find_headers_end(&raw).expect("headers end");
        let head = String::from_utf8_lossy(&raw[..headers_end]).to_string();
        let body = &raw[headers_end + 4..headers_end + 4 + content_length];

        let mut decoder = GzDecoder::new(body);
        let mut decoded = String::new();
        decoder
            .read_to_string(&mut decoded)
            .expect("decode gzip body");
        tx.send((head, decoded)).expect("send request capture");

        write_json_response(
            &mut stream,
            r#"{"accepted":1,"rejected":0,"invalid":0,"acks":[]}"#,
        );
        stream.flush().expect("flush response");
    });

    let endpoint = format!("http://{addr}/events");
    let logger = New(Config::test("checkout").with_sink(loza::HttpBatchSink(&endpoint)));
    let mut ctx = logger.start_event(Params::new("checkout.collector").with_kind("cli"));
    logger.enrich(&mut ctx, "tenant.id", "tenant-1");
    logger.finish(&mut ctx, "success").expect("finish event");
    logger.emit(&ctx).expect("emit via collector sink");

    let (head, decoded) = rx
        .recv_timeout(Duration::from_secs(5))
        .expect("captured request");
    handle.join().expect("server thread");

    assert!(head.starts_with("POST /events HTTP/1.1"));
    assert!(head.contains("Content-Type: application/json"));
    assert!(head.contains("Content-Encoding: gzip"));
    assert!(decoded.contains("\"events\":["));
    assert!(decoded.contains("\"event\":\"checkout.collector\""));
    assert!(decoded.contains("\"service\":\"checkout\""));
    // Regression guard: the sibling env-key test uses this exact value. If
    // env_lock discipline regresses, its transient key leaks into this request.
    assert!(
        !head.contains("secret-key"),
        "a sibling test's API key leaked through the process-global environment"
    );
}

#[test]
fn http_batch_sink_uses_api_key_from_environment() {
    let _lock = lock_env();
    let listener = TcpListener::bind("127.0.0.1:0").expect("bind test server");
    let addr = listener.local_addr().expect("listener addr");
    let (tx, rx) = mpsc::channel();

    let handle = thread::spawn(move || {
        let (mut stream, _) = listener.accept().expect("accept request");
        stream
            .set_read_timeout(Some(Duration::from_secs(5)))
            .expect("set timeout");
        let raw = read_http_request(&mut stream);
        let headers_end = find_headers_end(&raw).expect("headers end");
        let head = String::from_utf8_lossy(&raw[..headers_end]).to_string();
        tx.send(head).expect("send head");
        write_json_response(
            &mut stream,
            r#"{"accepted":1,"rejected":0,"invalid":0,"acks":[]}"#,
        );
    });

    // Guards own restoration, so a panic in the assertions below cannot leak
    // these process-wide variables into later tests.
    let _api_key = EnvRestore::set("LOZA_COLLECTOR_API_KEY", "secret-key");
    let _api_key_header = EnvRestore::set("LOZA_COLLECTOR_API_KEY_HEADER", "X-API-Key");

    let endpoint = format!("http://{addr}/events");
    let logger = New(Config::test("checkout").with_sink(loza::HttpBatchSink(&endpoint)));
    let mut ctx = logger.start_event(Params::new("checkout.collector").with_kind("cli"));
    logger.finish(&mut ctx, "success").expect("finish event");
    logger.emit(&ctx).expect("emit via collector sink");

    let head = rx
        .recv_timeout(Duration::from_secs(5))
        .expect("captured request");
    handle.join().expect("server thread");

    assert!(head.contains("X-API-Key: secret-key"));
}

#[test]
fn env_lock_serializes_global_collector_env_access() {
    // Regression for the cross-test env race: the collector API-key variables
    // are process-global and are read at emit time, so every HttpBatchSink
    // test must hold env_lock while it sets or observes them. This proves the
    // lock actually excludes a sibling while a transient key is present.
    let guard = env_lock().lock().expect("env lock");

    let (acquired_tx, acquired_rx) = mpsc::channel();
    let (release_tx, release_rx) = mpsc::channel::<()>();
    let contender = thread::spawn(move || {
        let _g = env_lock().lock().expect("env lock");
        acquired_tx.send(()).expect("signal lock acquired");
        release_rx.recv().expect("wait for release");
    });

    std::env::set_var("LOZA_COLLECTOR_API_KEY", "sibling-leaked-key");
    std::env::set_var("LOZA_COLLECTOR_API_KEY_HEADER", "X-API-Key");

    // While this test holds the lock with the key set, a sibling cannot
    // acquire it and construct/emit a sink that observes the transient value.
    assert!(
        acquired_rx
            .recv_timeout(Duration::from_millis(200))
            .is_err(),
        "sibling acquired env_lock while collector key env vars were set"
    );

    std::env::remove_var("LOZA_COLLECTOR_API_KEY");
    std::env::remove_var("LOZA_COLLECTOR_API_KEY_HEADER");
    drop(guard);

    release_tx.send(()).expect("release contender");
    acquired_rx
        .recv_timeout(Duration::from_secs(5))
        .expect("sibling acquires lock after release");
    contender.join().expect("contender thread");
}

fn find_headers_end(raw: &[u8]) -> Option<usize> {
    raw.windows(4).position(|window| window == b"\r\n\r\n")
}

fn read_http_request(stream: &mut std::net::TcpStream) -> Vec<u8> {
    let mut raw = Vec::new();
    let mut headers_done = false;
    let mut content_length = 0usize;
    loop {
        let mut chunk = [0u8; 1024];
        let n = stream.read(&mut chunk).expect("read request");
        if n == 0 {
            break;
        }
        raw.extend_from_slice(&chunk[..n]);
        if let Some(pos) = find_headers_end(&raw) {
            if !headers_done {
                headers_done = true;
                let head = String::from_utf8_lossy(&raw[..pos]);
                content_length = parse_content_length(&head);
            }
            let body_len = raw.len() - (pos + 4);
            if body_len >= content_length {
                break;
            }
        }
    }
    raw
}

fn parse_content_length(headers: &str) -> usize {
    headers
        .lines()
        .find_map(|line| {
            let (name, value) = line.split_once(':')?;
            if name.eq_ignore_ascii_case("content-length") {
                return value.trim().parse::<usize>().ok();
            }
            None
        })
        .unwrap_or(0)
}

fn write_json_response(stream: &mut std::net::TcpStream, body: &str) {
    let response = format!(
        "HTTP/1.1 202 Accepted\r\nContent-Type: application/json\r\nContent-Length: {}\r\n\r\n{}",
        body.len(),
        body
    );
    stream
        .write_all(response.as_bytes())
        .expect("write collector response");
}

/// Restores an environment variable to its prior value when dropped.
///
/// Capturing the previous value (rather than unconditionally removing the var)
/// also avoids clobbering variables already present in the test runner.
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

#[test]
fn env_restore_guard_resets_var_to_previous_value() {
    const KEY: &str = "LOZA_RS_ENV_RESTORE_PREVIOUS";
    let _lock = lock_env();
    std::env::set_var(KEY, "original");

    {
        let _override = EnvRestore::set(KEY, "temporary");
        assert_eq!(std::env::var(KEY).unwrap(), "temporary");
    }
    assert_eq!(std::env::var(KEY).unwrap(), "original");

    std::env::remove_var(KEY);
}

#[test]
fn env_restore_guard_resets_var_even_when_body_panics() {
    const KEY: &str = "LOZA_RS_ENV_RESTORE_PANIC";
    let _lock = lock_env();
    std::env::remove_var(KEY);

    let result = std::panic::catch_unwind(|| {
        let _override = EnvRestore::set(KEY, "temporary");
        panic!("intentional panic while env override is active");
    });
    assert!(result.is_err(), "the test body should have panicked");
    assert!(
        std::env::var_os(KEY).is_none(),
        "env var must be restored during unwinding"
    );
}

#[test]
fn poisoned_env_lock_can_still_be_acquired() {
    // Poison the shared mutex the same way a panicking test would.
    let outcome = thread::spawn(|| {
        let _guard = env_lock().lock().expect("acquire env lock");
        panic!("intentional panic to poison env lock");
    })
    .join();
    assert!(outcome.is_err(), "helper thread should poison the mutex");

    // A later test must not panic here just because the mutex was poisoned.
    let _guard = lock_env();
}
