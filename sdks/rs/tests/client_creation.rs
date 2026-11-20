#[test]
fn client_creation_and_alias_helpers_work() {
    let logger = loza::create_loza(loza::Config::test("catalog"));
    assert_eq!(logger.config().service, "catalog");

    let _ = loza::configure(loza::Config::test("default"));
    let aliased = loza::alias("audit");
    assert_eq!(aliased.config().service, "default");
    assert_eq!(aliased.config().alias, "audit");
}

#[test]
fn direct_basic_auth_rejects_remote_plaintext_http() {
    let client = loza::core::CollectorHttpClient::new("http://collector.example.com")
        .with_basic_auth("key-id", "secret");
    let error = client
        .ingest(&[])
        .expect_err("remote Basic auth must not use plaintext HTTP");
    assert!(
        error.contains("credentialed HTTP requires TLS"),
        "unexpected error: {error}"
    );
}

#[test]
fn direct_api_key_auth_rejects_remote_plaintext_http() {
    let client = loza::core::CollectorHttpClient::new("http://collector.example.com")
        .with_api_key("lz_sec_live_secret");
    let error = client
        .ingest(&[])
        .expect_err("remote Bearer API key auth must not use plaintext HTTP");
    assert!(
        error.contains("credentialed HTTP requires TLS"),
        "unexpected error: {error}"
    );
}

#[test]
fn direct_api_key_auth_allows_loopback_plaintext_http() {
    use std::io::{Read, Write};
    use std::net::TcpListener;
    use std::thread;

    let listener = TcpListener::bind("127.0.0.1:0").expect("bind test server");
    let addr = listener.local_addr().expect("local addr");
    let handle = thread::spawn(move || {
        let (mut stream, _) = listener.accept().expect("accept");
        let mut buf = [0_u8; 1024];
        let _ = stream.read(&mut buf);
        let body = r#"{"status":"ok"}"#;
        let response = format!(
            "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
            body.len(),
            body
        );
        let _ = stream.write_all(response.as_bytes());
    });

    let client = loza::core::CollectorHttpClient::new(format!("http://{addr}"))
        .with_api_key("lz_sec_live_secret")
        .with_timeout_ms(1_000);
    let response = client
        .health()
        .expect("loopback Bearer auth over HTTP must stay allowed");
    assert_eq!(response.status_code, 200);
    handle.join().expect("server thread");
}

#[test]
fn config_rejects_api_key_over_plaintext_http() {
    let remote = loza::Config::dev("catalog")
        .with_collector_endpoint("http://collector.example.com:80")
        .with_api_key("lz_sec_live_key");
    assert!(remote.validate().is_err());

    // A DSN with tls=false resolves to a plaintext endpoint and is rejected.
    let dsn = loza::Config::dev("catalog")
        .with_collector_endpoint("loza://collector.example.com/events?tls=false")
        .with_api_key("lz_sec_live_key");
    assert!(dsn.validate().is_err());

    // Loopback remains permitted for local development.
    let local = loza::Config::dev("catalog")
        .with_collector_endpoint("http://localhost:9308")
        .with_api_key("local-key");
    assert!(local.validate().is_ok());
}
