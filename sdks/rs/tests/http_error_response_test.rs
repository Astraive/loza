use loza::core::client::{HTTPClient, HTTPRequest};
use loza::internal::transport::{HttpRequest, Transport};
use std::io::{Read, Write};
use std::net::TcpListener;
use std::thread;

/// Spin up a one-shot local server that replies with an HTTP error status,
/// response headers and a JSON error payload.
fn spawn_error_server(
    status_line: &'static str,
    body: &'static str,
) -> (String, thread::JoinHandle<()>) {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let server = thread::spawn(move || {
        let (mut stream, _) = listener.accept().unwrap();
        let mut request = Vec::new();
        let mut chunk = [0_u8; 512];
        loop {
            let count = stream.read(&mut chunk).unwrap();
            if count == 0 {
                break;
            }
            request.extend_from_slice(&chunk[..count]);
            if let Some(header_end) = request.windows(4).position(|w| w == b"\r\n\r\n") {
                let headers = String::from_utf8_lossy(&request[..header_end]);
                let body_len = headers
                    .lines()
                    .find_map(|line| line.strip_prefix("Content-Length:"))
                    .and_then(|value| value.trim().parse::<usize>().ok())
                    .unwrap_or(0);
                if request.len() >= header_end + 4 + body_len {
                    break;
                }
            }
        }
        let response = format!(
            "HTTP/1.1 {status_line}\r\nContent-Type: application/json\r\nX-Request-Id: err-req\r\nX-Deduped: 0\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
            body.len()
        );
        stream.write_all(response.as_bytes()).unwrap();
        stream.flush().unwrap();
    });
    (format!("http://{address}"), server)
}

#[test]
fn transport_returns_4xx_response_with_status_headers_and_body() {
    let body = r#"{"error":"rate_limited","message":"slow down"}"#;
    let (url, server) = spawn_error_server("429 Too Many Requests", body);

    let response = Transport::with_timeout_ms(2_000)
        .send(&HttpRequest::new("GET", &url))
        .expect("a 4xx response must be returned as Ok, not discarded as an io::Error");

    assert_eq!(response.status_code, 429);
    assert_eq!(response.body, body);
    assert_eq!(response.headers["content-type"], "application/json");
    assert_eq!(response.headers["x-request-id"], "err-req");
    assert_eq!(response.headers["x-deduped"], "0");
    server.join().unwrap();
}

#[test]
fn http_client_returns_5xx_response_with_status_headers_and_body() {
    let body = r#"{"error":"internal","message":"boom"}"#;
    let (url, server) = spawn_error_server("503 Service Unavailable", body);

    let response = HTTPClient::with_timeout_ms(2_000)
        .send(&HTTPRequest::new("POST", &url).with_body("{}"))
        .expect("a 5xx response must be returned as Ok, not discarded as an io::Error");

    assert_eq!(response.status_code, 503);
    assert_eq!(response.body, body);
    assert_eq!(response.headers["content-type"], "application/json");
    assert_eq!(response.headers["x-request-id"], "err-req");
    server.join().unwrap();
}
