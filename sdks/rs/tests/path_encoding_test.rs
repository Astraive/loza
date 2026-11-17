use loza::{CollectorHttpClient, QueryValue};
use std::collections::HashMap;
use std::io::{Read, Write};
use std::net::TcpListener;
use std::sync::mpsc;
use std::thread;
use std::time::Duration;

/// Minimal HTTP server that records the raw request target of each connection
/// and replies with a body valid for every collector API used in this test.
fn start_path_capture(
    connections: usize,
) -> (String, mpsc::Receiver<String>, thread::JoinHandle<()>) {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let (tx, rx) = mpsc::channel();

    let server = thread::spawn(move || {
        for _ in 0..connections {
            let (mut stream, _) = match listener.accept() {
                Ok(pair) => pair,
                Err(_) => break,
            };
            let _ = stream.set_read_timeout(Some(Duration::from_secs(2)));

            let mut request = Vec::new();
            let mut byte = [0_u8; 1];
            while !request.ends_with(b"\r\n\r\n") {
                match stream.read_exact(&mut byte) {
                    Ok(()) => request.push(byte[0]),
                    Err(_) => break,
                }
            }
            let path = String::from_utf8_lossy(&request)
                .lines()
                .next()
                .and_then(|line| line.split_whitespace().nth(1))
                .unwrap_or("")
                .to_string();
            let _ = tx.send(path);

            let body = r#"{"columns":["event_id"],"rows":[],"row_count":0,"ok":true}"#;
            let response = format!(
                "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
                body.len(),
                body
            );
            let _ = stream.write_all(response.as_bytes());
            let _ = stream.flush();
        }
    });

    (format!("http://{address}"), rx, server)
}

fn recorder(base: &str) -> CollectorHttpClient {
    CollectorHttpClient::new(base)
        .with_api_key("api")
        .with_timeout_ms(1_000)
}

#[test]
fn untrusted_path_segments_are_percent_encoded() {
    let (base, rx, server) = start_path_capture(8);
    let client = recorder(&base);

    client.delete("../keys").unwrap();
    client.delete("?x=1").unwrap();
    client.delete("evt-1.2_3~4").unwrap();
    client.dlq_read("../keys").unwrap();
    client.keys_revoke("a#b").unwrap();
    client.keys_rotate("../keys").unwrap();
    client.sinks_test("a?x=1").unwrap();
    recorder(&base)
        .with_collector("../other")
        .query_lql("from events", HashMap::<String, QueryValue>::new(), 10)
        .unwrap();

    let expected = [
        "/events/..%2Fkeys",
        "/events/%3Fx%3D1",
        "/events/evt-1.2_3~4",
        "/dlq/..%2Fkeys",
        "/keys/a%23b",
        "/keys/..%2Fkeys/rotate",
        "/sinks/a%3Fx%3D1/test",
        "/collectors/..%2Fother/lql/query",
    ];

    for want in expected {
        let got = rx
            .recv_timeout(Duration::from_secs(5))
            .expect("server should record a request path");
        assert_eq!(got, want, "request target must not be attacker-controlled");
    }
    server.join().unwrap();
}

#[test]
fn bare_dot_segments_are_rejected_without_sending_a_request() {
    let client = recorder("http://127.0.0.1:1");

    assert!(client.delete("..").is_err());
    assert!(client.delete(".").is_err());
    assert!(client.dlq_read("..").is_err());
    assert!(client.keys_revoke("..").is_err());
    assert!(client.keys_rotate("..").is_err());
    assert!(client.sinks_test("..").is_err());

    let scoped = recorder("http://127.0.0.1:1").with_collector("..");
    let error = scoped
        .query_lql("from events", HashMap::<String, QueryValue>::new(), 10)
        .expect_err("dot-segment collector name must be rejected");
    assert_eq!(error.category, "invalid_configuration");
}
