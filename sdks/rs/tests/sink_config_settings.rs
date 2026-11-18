use loza::{Config, New, Params, SinkConfig};
use std::io::{self, Read, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::mpsc;
use std::thread;
use std::time::{Duration, Instant};

const SUCCESS: &str = r#"{"accepted":1,"rejected":0,"invalid":0,"acks":[]}"#;
const RETRYABLE: &str = r#"{"accepted":0,"rejected":0,"invalid":0,"acks":[],"errors":[{"code":"RATE_LIMIT","message":"slow down","retryable":true}]}"#;

fn http_sink(
    endpoint: String,
    max_retries: u32,
    enable_compression: bool,
    max_batch_bytes: usize,
) -> SinkConfig {
    SinkConfig::HttpBatch {
        endpoint,
        api_key: None,
        basic_username: None,
        basic_password: None,
        insecure: false,
        timeout_ms: 2_000,
        max_batch_bytes,
        max_retries,
        enable_compression,
        ndjson: false,
    }
}

fn find_headers_end(raw: &[u8]) -> Option<usize> {
    raw.windows(4).position(|window| window == b"\r\n\r\n")
}

fn parse_content_length(head: &str) -> usize {
    head.lines()
        .find_map(|line| {
            let (name, value) = line.split_once(':')?;
            if name.eq_ignore_ascii_case("content-length") {
                return value.trim().parse::<usize>().ok();
            }
            None
        })
        .unwrap_or(0)
}

fn read_request(stream: &mut TcpStream) -> Vec<u8> {
    let mut raw = Vec::new();
    let mut chunk = [0u8; 4096];
    let mut content_length = 0usize;
    loop {
        let n = match stream.read(&mut chunk) {
            Ok(n) => n,
            Err(_) => break,
        };
        if n == 0 {
            break;
        }
        raw.extend_from_slice(&chunk[..n]);
        if let Some(pos) = find_headers_end(&raw) {
            if content_length == 0 {
                content_length = parse_content_length(&String::from_utf8_lossy(&raw[..pos]));
            }
            if raw.len() >= pos + 4 + content_length {
                break;
            }
        }
    }
    raw
}

fn write_response(stream: &mut TcpStream, status: u16, reason: &str, body: &str) {
    let response = format!(
        "HTTP/1.1 {status} {reason}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
        body.len(),
        body
    );
    stream
        .write_all(response.as_bytes())
        .expect("write response");
    stream.flush().expect("flush response");
}

/// Accept requests until no new one arrives for a short grace period.
fn serve_requests(
    listener: TcpListener,
    body: &'static str,
    status: u16,
    reason: &'static str,
) -> usize {
    listener
        .set_nonblocking(true)
        .expect("set listener nonblocking");
    let mut count = 0;
    let mut last_request: Option<Instant> = None;
    loop {
        match listener.accept() {
            Ok((mut stream, _)) => {
                stream.set_read_timeout(Some(Duration::from_secs(1))).ok();
                let _ = read_request(&mut stream);
                write_response(&mut stream, status, reason, body);
                count += 1;
                last_request = Some(Instant::now());
            }
            Err(err) if err.kind() == io::ErrorKind::WouldBlock => {
                if last_request
                    .map(|seen| seen.elapsed() >= Duration::from_millis(400))
                    .unwrap_or(false)
                {
                    break;
                }
                thread::sleep(Duration::from_millis(5));
            }
            Err(_) => break,
        }
        if count >= 5 {
            break;
        }
    }
    count
}

#[test]
fn configured_compression_setting_controls_request_encoding() {
    let listener = TcpListener::bind("127.0.0.1:0").expect("bind test server");
    let addr = listener.local_addr().expect("listener addr");
    let (tx, rx) = mpsc::channel();

    let server = thread::spawn(move || {
        let (mut stream, _) = listener.accept().expect("accept request");
        stream
            .set_read_timeout(Some(Duration::from_secs(5)))
            .expect("set timeout");
        let raw = read_request(&mut stream);
        let head_end = find_headers_end(&raw).expect("headers end");
        let head = String::from_utf8_lossy(&raw[..head_end]).to_string();
        let body_start = head_end + 4;
        let body_end = body_start + parse_content_length(&head);
        let body = String::from_utf8_lossy(&raw[body_start..body_end]).to_string();
        tx.send((head, body)).expect("send capture");
        write_response(&mut stream, 202, "Accepted", SUCCESS);
    });

    let endpoint = format!("http://{addr}/events");
    let sink = http_sink(endpoint, 3, false, 256 * 1024);
    let logger = New(Config::test("checkout").with_sink(sink));
    let mut ctx = logger.start_event(Params::new("checkout.collector").with_kind("cli"));
    logger.finish(&mut ctx, "success").expect("finish event");
    logger.emit(&ctx).expect("emit event");

    let (head, body) = rx
        .recv_timeout(Duration::from_secs(5))
        .expect("captured request");
    server.join().expect("server thread");

    assert!(head.starts_with("POST /events HTTP/1.1"));
    assert!(head.contains("Content-Type: application/json"));
    assert!(!head.to_ascii_lowercase().contains("content-encoding"));
    assert!(body.contains("\"events\":["));
    assert!(body.contains("\"checkout.collector\""));
}

#[test]
fn configured_max_retries_controls_delivery_attempts() {
    let listener = TcpListener::bind("127.0.0.1:0").expect("bind test server");
    let addr = listener.local_addr().expect("listener addr");
    let (tx, rx) = mpsc::channel();

    let server = thread::spawn(move || {
        let count = serve_requests(listener, RETRYABLE, 503, "Service Unavailable");
        tx.send(count).expect("send attempt count");
    });

    let endpoint = format!("http://{addr}/events");
    let sink = http_sink(endpoint, 2, false, 256 * 1024);
    let logger = New(Config::test("checkout").with_sink(sink));
    let mut ctx = logger.start_event(Params::new("checkout.collector").with_kind("cli"));
    logger.finish(&mut ctx, "success").expect("finish event");
    // All attempts are rejected as retryable, so delivery ultimately fails.
    let _ = logger.emit(&ctx);

    let attempts = rx
        .recv_timeout(Duration::from_secs(5))
        .expect("attempt count");
    server.join().expect("server thread");
    assert_eq!(attempts, 2);
}

#[test]
fn configured_max_batch_bytes_splits_batched_delivery() {
    let listener = TcpListener::bind("127.0.0.1:0").expect("bind test server");
    let addr = listener.local_addr().expect("listener addr");
    let (tx, rx) = mpsc::channel();

    let server = thread::spawn(move || {
        let count = serve_requests(listener, SUCCESS, 202, "Accepted");
        tx.send(count).expect("send request count");
    });

    let endpoint = format!("http://{addr}/events");
    let sink = http_sink(endpoint, 3, false, 1);
    let logger = New(Config::test("checkout").with_sink(sink).with_async(true));
    for name in ["checkout.collector", "checkout.other", "checkout.third"] {
        let mut ctx = logger.start_event(Params::new(name).with_kind("cli"));
        logger.finish(&mut ctx, "success").expect("finish event");
        logger.emit(&ctx).expect("emit event");
    }
    logger.flush().expect("flush batched events");

    let requests = rx
        .recv_timeout(Duration::from_secs(5))
        .expect("request count");
    server.join().expect("server thread");
    assert_eq!(requests, 3);
}
