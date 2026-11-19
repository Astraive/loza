use loza::{Config, MemorySinkStore, SinkConfig};

#[test]
fn set_global_logger_shuts_down_previous_logger_and_flushes_pending_events() {
    // With async delivery enabled, emitted events are buffered in the worker's
    // batcher until a flush/shutdown command arrives. A memory sink lets us
    // observe whether those buffered events survive a global-logger swap.
    let store = MemorySinkStore::new();
    let old_logger = loza::create_loza(
        Config::test("pending-events")
            .with_async(true)
            .with_sink(SinkConfig::Memory(store.clone())),
    );
    loza::set_global_logger(old_logger);

    // Emit through the global logger. These events enter the async batcher and
    // are not written to the memory sink yet (the batch is far from full).
    loza::Info("pending-event-one");
    loza::Info("pending-event-two");
    assert!(
        store.events().is_empty(),
        "precondition: async events should still be buffered before replacement"
    );

    // Replacing the global logger must shut down the previous one first,
    // flushing its buffered events instead of dropping them when its
    // AsyncRuntime sender is destroyed.
    let replacement = loza::create_loza(Config::test("replacement"));
    loza::set_global_logger(replacement);

    let events = store.events();
    assert!(
        events.iter().any(|e| e.contains("pending-event-one")),
        "set_global_logger dropped a pending event: {events:?}"
    );
    assert!(
        events.iter().any(|e| e.contains("pending-event-two")),
        "set_global_logger dropped a pending event: {events:?}"
    );
}
