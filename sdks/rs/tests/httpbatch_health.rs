use loza::sinks::HttpBatchSinkConfig;
use std::io::{Read, Write};
use std::net::TcpListener;
use std::sync::mpsc;
use std::sync::{Mutex, OnceLock};
use std::thread;
use std::time::Duration;

fn env_lock() -> std::sync::MutexGuard<'static, ()> {
    static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
    LOCK.get_or_init(|| Mutex::new(()))
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

/// One-shot collector that requires a header on `/healthz` before reporting healthy.
fn one_shot_health_server(
    required_header: Option<(&str, &str)>,
) -> (String, mpsc::Receiver<String>, thread::JoinHandle<()>) {
    let listener = TcpListener::bind("127.0.0.1:0").expect("bind test server");
    let addr = listener.local_addr().expect("listener addr");
    let (tx, rx) = mpsc::channel();
    let required = required_header.map(|(name, value)| (name.to_string(), value.to_string()));

    let handle = thread::spawn(move || {
        let (mut stream, _) = listener.accept().expect("accept health probe");
        stream
            .set_read_timeout(Some(Duration::from_secs(5)))
            .expect("set read timeout");

        let mut raw = Vec::new();
        let mut chunk = [0u8; 1024];
        while !raw.windows(4).any(|window| window == b"\r\n\r\n") {
            let n = stream.read(&mut chunk).expect("read health probe");
            if n == 0 {
                break;
            }
            raw.extend_from_slice(&chunk[..n]);
        }
        let head = String::from_utf8_lossy(&raw).to_string();
        tx.send(head.clone()).expect("send captured request");

        let authorized = required.as_ref().is_none_or(|(name, value)| {
            head.lines().any(|line| {
                let mut parts = line.splitn(2, ':');
                let matches_name = parts
                    .next()
                    .map(|found| found.eq_ignore_ascii_case(name))
                    .unwrap_or(false);
                let matches_value = parts
                    .next()
                    .map(|found| found.trim() == value)
                    .unwrap_or(false);
                matches_name && matches_value
            })
        });
        let (status, body) = if authorized {
            ("200 OK", "ok")
        } else {
            ("401 Unauthorized", "unauthorized")
        };
        let response = format!(
            "HTTP/1.1 {status}\r\nContent-Type: text/plain\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
            body.len()
        );
        stream
            .write_all(response.as_bytes())
            .expect("write health response");
        stream.flush().expect("flush response");
    });

    (format!("http://{addr}"), rx, handle)
}

#[test]
fn health_probe_sends_configured_api_key() {
    let _guard = env_lock();
    std::env::remove_var("LOZA_COLLECTOR_API_KEY");
    std::env::remove_var("LOZA_COLLECTOR_API_KEY_HEADER");

    let (endpoint, rx, handle) =
        one_shot_health_server(Some(("authorization", "Bearer health-key")));
    let sink = HttpBatchSinkConfig::new(endpoint)
        .with_api_key("health-key")
        .with_insecure(true)
        .as_sink_config();

    assert!(
        loza::health(&sink),
        "health probe must authenticate like writes do"
    );

    let head = rx
        .recv_timeout(Duration::from_secs(5))
        .expect("captured health request");
    handle.join().expect("server thread");
    assert!(head.starts_with("GET /healthz HTTP/1.1"));
    assert!(head
        .lines()
        .any(|line| line.eq_ignore_ascii_case("authorization: Bearer health-key")));
}

#[test]
fn health_probe_sends_basic_auth() {
    let _guard = env_lock();
    std::env::remove_var("LOZA_COLLECTOR_API_KEY");
    std::env::remove_var("LOZA_COLLECTOR_API_KEY_HEADER");

    let (endpoint, rx, handle) =
        one_shot_health_server(Some(("authorization", "Basic dXNlcjpwYXNz")));
    let sink = HttpBatchSinkConfig::new(endpoint)
        .with_basic_auth("user", "pass")
        .with_insecure(true)
        .as_sink_config();

    assert!(
        loza::health(&sink),
        "health probe must send basic auth like writes do"
    );

    let head = rx
        .recv_timeout(Duration::from_secs(5))
        .expect("captured health request");
    handle.join().expect("server thread");
    assert!(head
        .lines()
        .any(|line| line.eq_ignore_ascii_case("authorization: Basic dXNlcjpwYXNz")));
}

#[test]
fn health_probe_uses_env_api_key_and_header() {
    let _guard = env_lock();
    std::env::set_var("LOZA_COLLECTOR_API_KEY", "env-health-key");
    std::env::set_var("LOZA_COLLECTOR_API_KEY_HEADER", "X-API-Key");

    let (endpoint, rx, handle) = one_shot_health_server(Some(("x-api-key", "env-health-key")));
    let sink = HttpBatchSinkConfig::new(endpoint)
        .with_insecure(true)
        .as_sink_config();

    let healthy = loza::health(&sink);
    std::env::remove_var("LOZA_COLLECTOR_API_KEY");
    std::env::remove_var("LOZA_COLLECTOR_API_KEY_HEADER");

    assert!(
        healthy,
        "health probe must use env credentials like writes do"
    );

    let head = rx
        .recv_timeout(Duration::from_secs(5))
        .expect("captured health request");
    handle.join().expect("server thread");
    assert!(head
        .lines()
        .any(|line| line.eq_ignore_ascii_case("x-api-key: env-health-key")));
}
