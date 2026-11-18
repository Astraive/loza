use loza::{Config, New, Params, StatsHandler};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;

#[derive(Default)]
struct RecordingHandler {
    drops: AtomicUsize,
    delivery_failures: AtomicUsize,
}

impl StatsHandler for RecordingHandler {
    fn on_emit(&self, _event: &loza::EventContext) {}

    fn on_drop(&self, _reason: &str) {
        self.drops.fetch_add(1, Ordering::SeqCst);
    }

    fn on_error(&self, _error: &str) {}

    fn on_delivery_failed(&self, _event: &loza::EventContext, _error: &str) {
        self.delivery_failures.fetch_add(1, Ordering::SeqCst);
    }
}

#[test]
fn full_batch_delivery_failure_is_reported_instead_of_swallowed() {
    // A unique HttpBatch endpoint gives this paused sink a distinct Debug key so
    // the global sink-pause registry cannot affect other tests. Being paused
    // makes write_batch_sink fail before any network activity occurs.
    let endpoint = "http://127.0.0.1:1/async-delivery-failure-regression";
    let sink = loza::HttpBatchSink(endpoint);
    loza::pause(&sink);

    let handler = Arc::new(RecordingHandler::default());
    let logger = New(Config::test("async-delivery")
        .with_async(true)
        .with_sink(sink.clone())
        .with_stats_handler(handler.clone()));

    // Each encoded event is ~150 KiB; two of them exceed the 256 KiB batch limit
    // and force the worker's fire-and-forget Event path to flush a full batch.
    let payload = "x".repeat(150 * 1024);
    for _ in 0..2 {
        let mut ctx = logger.start_event(
            Params::new("async.delivery")
                .with_kind("cli")
                .with_message(payload.clone()),
        );
        logger.finish(&mut ctx, "success").expect("finish event");
        // Enqueue succeeds; the sink write is attempted later on the worker.
        logger.emit(&ctx).expect("emit should succeed on enqueue");
    }

    // Commands are processed in order, so once this flush command is acknowledged
    // the earlier full-batch delivery attempt has completed. The flush itself
    // also fails against the paused sink, which is expected and ignored here.
    let _ = logger.flush();

    let snapshot = logger.metrics().snapshot();
    assert!(
        snapshot
            .events_dropped_by_reason
            .get("transport")
            .copied()
            .unwrap_or(0)
            >= 1,
        "failed async delivery must be recorded as a transport drop: {snapshot:?}"
    );
    assert!(
        snapshot.events_emitted_failure >= 1,
        "failed async delivery must be recorded as an emitted failure: {snapshot:?}"
    );
    assert!(
        handler.drops.load(Ordering::SeqCst) >= 1,
        "failed async delivery must notify on_drop"
    );
    assert!(
        handler.delivery_failures.load(Ordering::SeqCst) >= 1,
        "failed async delivery must notify on_delivery_failed"
    );

    loza::resume(&sink);
    let _ = logger.shutdown();
}
