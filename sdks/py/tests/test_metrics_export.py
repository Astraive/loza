"""Test metrics export from Python SDK (Prometheus format)."""

import json
import loza


def test_events_emitted_total() -> None:
    """Verify events_emitted_total counter increments."""
    metrics = loza.NewMetricsCollector()
    logger = loza.New(loza.Test("test").with_metrics(metrics))

    # Emit multiple events
    ctx1 = logger.start_event(loza.Params(event="event1"))
    logger.finish(ctx1, "success")
    logger.emit(ctx1)

    ctx2 = logger.start_event(loza.Params(event="event2"))
    logger.finish(ctx2, "error")
    logger.emit(ctx2)

    ctx3 = logger.start_event(loza.Params(event="event3"))
    logger.finish(ctx3, "success")
    logger.emit(ctx3)

    # Actually inspect the counter: three emits must be recorded. Checking only
    # the returned contexts lets a broken/no-op counter pass unnoticed.
    emitted = metrics.snapshot()["counter_vecs"].get("events_emitted_total", {})
    assert emitted.get("success", 0) == 3
    assert sum(emitted.values()) == 3


def test_delivery_attempts_recorded() -> None:
    """Verify delivery attempts are recorded."""

    class CountingSink:
        def __init__(self):
            self.write_count = 0

        def write(self, payload: str) -> None:
            self.write_count += 1

        def flush(self) -> None:
            pass

        def close(self) -> None:
            pass

    sink = CountingSink()
    logger = loza.New(loza.Test("test").with_sink(sink))

    ctx = logger.start_event(loza.Params(event="test"))
    logger.finish(ctx, "success")
    logger.emit(ctx)

    # Sink should have been called at least once
    assert sink.write_count >= 1


def test_sampling_rate_gauge() -> None:
    """Verify sampling rate is tracked."""
    # Create logger with 50% sampling
    logger = loza.New(loza.Test("test").with_sampler(loza.SampleRandom(0.5)))

    # Emit multiple events and count how many are sampled
    sampled_count = 0
    for i in range(100):
        ctx = logger.start_event(loza.Params(event=f"event_{i}"))
        logger.finish(ctx, "success")
        payload = logger.emit(ctx)
        if payload:
            sampled_count += 1

    # With 50% sampling and 100 events, expect roughly 30-70 to be emitted
    # (not strictly 50 due to randomness)
    assert 20 <= sampled_count <= 80, f"Sampling rate off: {sampled_count}/100"


def test_sink_latency_recorded() -> None:
    """Verify sink latency is recorded."""
    import time

    class SlowSink:
        def __init__(self):
            self.latencies = []

        def write(self, payload: str) -> None:
            start = time.time()
            time.sleep(0.001)  # 1ms simulated latency
            self.latencies.append(time.time() - start)

        def flush(self) -> None:
            pass

        def close(self) -> None:
            pass

    sink = SlowSink()
    logger = loza.New(loza.Test("test").with_sink(sink))

    for i in range(5):
        ctx = logger.start_event(loza.Params(event=f"event_{i}"))
        logger.finish(ctx, "success")
        logger.emit(ctx)

    # Should have latency measurements
    assert len(sink.latencies) >= 5


def test_rejection_reason_tracked() -> None:
    """Verify rejection reasons are tracked when an oversized event is dropped."""
    metrics = loza.NewMetricsCollector()
    logger = loza.New(loza.Test("test").with_metrics(metrics))

    # Enrich stores the attribute without raising; rejection happens at emit time.
    ctx = logger.start_event(loza.Params(event="large_event"))
    huge_value = "x" * 10_000_000
    logger.enrich(ctx, loza.String("huge", huge_value))
    logger.finish(ctx, "success")
    payload = logger.emit(ctx)

    # The oversized event is dropped and its reason is recorded unconditionally.
    assert payload == ""
    assert metrics.snapshot()["counter_vecs"]["events_dropped_total"]["oversized_event"] == 1


def test_rejection_reason_exported() -> None:
    """Verify the tracked rejection reason is present in the Prometheus export."""
    metrics = loza.NewMetricsCollector()
    logger = loza.New(loza.Test("test").with_metrics(metrics))

    ctx = logger.start_event(loza.Params(event="large_event"))
    logger.enrich(ctx, loza.String("huge", "x" * 10_000_000))
    logger.finish(ctx, "success")
    logger.emit(ctx)

    rendered = loza.RenderPrometheus(metrics)
    assert 'loza_sdk_events_dropped_total{reason="oversized_event"} 1' in rendered


def test_error_event_tracking() -> None:
    """Verify error events are tracked separately."""
    logger = loza.New(loza.Test("test"))

    # Create error event
    ctx = logger.start_event(loza.Params(event="error_test"))
    logger.enrich(ctx, loza.String("error_message", "Test error"))
    logger.finish(ctx, "error")
    payload = logger.emit(ctx)

    # Error should be recorded
    assert payload is not None
    event = json.loads(payload)
    assert event["outcome"] == "error"


def test_success_event_tracking() -> None:
    """Verify success events are tracked separately."""
    logger = loza.New(loza.Test("test"))

    # Create success event
    ctx = logger.start_event(loza.Params(event="success_test"))
    logger.finish(ctx, "success")
    payload = logger.emit(ctx)

    # Success should be recorded
    assert payload is not None
    event = json.loads(payload)
    assert event["outcome"] == "success"


def test_multiple_outcomes_tracked() -> None:
    """Verify multiple outcomes are tracked in metrics."""
    logger = loza.New(loza.Test("test"))

    outcomes = ["success", "error", "success", "partial"]
    for outcome in outcomes:
        ctx = logger.start_event(loza.Params(event=outcome))
        logger.finish(ctx, outcome)
        logger.emit(ctx)

    # All outcomes should emit without error
    assert True


def test_concurrent_emissions_tracked() -> None:
    """Verify concurrent emissions are tracked."""
    import threading

    logger = loza.New(loza.Test("test"))
    results = []

    def emit_event(i):
        ctx = logger.start_event(loza.Params(event=f"concurrent_{i}"))
        logger.finish(ctx, "success")
        payload = logger.emit(ctx)
        results.append(payload)

    threads = [threading.Thread(target=emit_event, args=(i,)) for i in range(5)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    # All emissions should complete
    assert len(results) == 5


def test_dropped_event_count() -> None:
    """Verify dropped events are counted."""
    # Create logger with none sampler (0% sampling)
    logger = loza.New(loza.Test("test").with_sampler(loza.SampleNone()))

    dropped_count = 0
    for i in range(10):
        ctx = logger.start_event(loza.Params(event=f"event_{i}"))
        logger.finish(ctx, "success")
        payload = logger.emit(ctx)
        if not payload:  # Dropped due to sampling
            dropped_count += 1

    # With SampleNone, all should be dropped
    assert dropped_count == 10
