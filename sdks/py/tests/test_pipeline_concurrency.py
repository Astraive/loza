"""Regression tests for pipeline stat-counter races and overflow reporting.

Counters are written by worker threads and by producers calling ``enqueue()``,
so every increment and every read must be synchronized. On a full queue the
event is either buffered for a later replay or genuinely dropped; those two
outcomes must be reported separately and must not be counted as emitted.
"""

from __future__ import annotations

import json
from dataclasses import replace
from threading import Lock, Thread

import pytest

import loza
from loza.core import pipeline as pipeline_module
from loza.core.client import Logger as ClientLogger
from loza.core.logger import Logger as EventLogger
from loza.core.metric import MetricsCollector
from loza.core.pipeline import (
    EnqueueOutcome,
    MemoryOfflineBuffer,
    Pipeline,
    RetryPolicy,
)


class _RecordingSink:
    def __init__(self) -> None:
        self._lock = Lock()
        self.events: list[str] = []

    def write_batch(self, encoded_events: list[str]) -> None:
        with self._lock:
            self.events.extend(encoded_events)


def _run_threads(target, count: int, *args) -> None:
    threads = [Thread(target=target, args=args) for _ in range(count)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()


def test_enqueued_counter_is_accurate_with_concurrent_producers() -> None:
    pipe = Pipeline([_RecordingSink()], queue_size=10_000)
    per_thread = 500
    thread_count = 8

    def produce(worker: int) -> None:
        for index in range(per_thread):
            assert pipe.enqueue(json.dumps({"worker": worker, "index": index})) is EnqueueOutcome.QUEUED

    producers = [Thread(target=produce, args=(worker,)) for worker in range(thread_count)]
    for thread in producers:
        thread.start()
    for thread in producers:
        thread.join()

    snapshot = pipe.stats.snapshot()
    assert snapshot["enqueued"] == per_thread * thread_count
    assert snapshot["buffered"] == 0
    assert snapshot["dropped"] == 0
    assert pipe.queue.qsize() == per_thread * thread_count


def test_emitted_and_batch_counters_are_accurate_with_concurrent_writers() -> None:
    sink = _RecordingSink()
    pipe = Pipeline([sink], retry_policy=RetryPolicy(max_attempts=1, base_delay=0))
    per_thread = 250
    thread_count = 6

    def write() -> None:
        for _ in range(per_thread):
            pipe.write_sync('{"service":"concurrent"}')

    _run_threads(write, thread_count)

    snapshot = pipe.stats.snapshot()
    assert snapshot["emitted"] == per_thread * thread_count
    assert snapshot["batches"] == per_thread * thread_count
    assert snapshot["failed"] == 0
    assert snapshot["retried"] == 0
    assert len(sink.events) == per_thread * thread_count


def test_snapshot_is_safe_and_consistent_while_counters_change() -> None:
    pipe = Pipeline([_RecordingSink()], queue_size=10_000)
    per_thread = 300
    thread_count = 4
    snapshots: list[dict[str, int | str]] = []
    snapshots_lock = Lock()

    def produce() -> None:
        for index in range(per_thread):
            pipe.enqueue(f'{{"index":{index}}}')

    def observe() -> None:
        for _ in range(per_thread):
            with snapshots_lock:
                snapshots.append(pipe.stats.snapshot())

    writers = [Thread(target=produce) for _ in range(thread_count)]
    reader = Thread(target=observe)
    for thread in [*writers, reader]:
        thread.start()
    for thread in [*writers, reader]:
        thread.join()

    assert snapshots
    for snapshot in snapshots:
        assert isinstance(snapshot["enqueued"], int)
        assert 0 <= snapshot["enqueued"] <= per_thread * thread_count
        assert snapshot["emitted"] == 0
        assert snapshot["buffered"] == 0
        assert snapshot["dropped"] == 0
    assert pipe.stats.snapshot()["enqueued"] == per_thread * thread_count


def test_failure_retry_and_error_counters_survive_concurrent_snapshots() -> None:
    class _FailingSink:
        def write_batch(self, _encoded_events: list[str]) -> None:
            raise RuntimeError("sink down")

    pipe = Pipeline(
        [_FailingSink()],
        retry_policy=RetryPolicy(max_attempts=2, base_delay=0),
        offline_buffer=MemoryOfflineBuffer(100),
    )
    snapshots: list[dict[str, int | str]] = []
    snapshots_lock = Lock()
    writes = 100

    def write() -> None:
        for _ in range(writes):
            pipe.write_sync('{"service":"failing"}')

    def observe() -> None:
        for _ in range(writes):
            with snapshots_lock:
                snapshots.append(pipe.stats.snapshot())

    writer = Thread(target=write)
    reader = Thread(target=observe)
    writer.start()
    reader.start()
    writer.join()
    reader.join()

    snapshot = pipe.stats.snapshot()
    assert snapshot["failed"] == writes
    assert snapshot["batches"] == writes
    assert snapshot["retried"] == writes
    assert snapshot["emitted"] == 0
    assert snapshot["last_error"] == "sink down"
    for observed in snapshots:
        assert 0 <= observed["failed"] <= writes
        assert 0 <= observed["batches"] <= writes


def test_overflow_without_buffer_reports_dropped() -> None:
    metrics = MetricsCollector()
    pipe = Pipeline([_RecordingSink()], queue_size=1, metrics=metrics)

    assert pipe.enqueue("first") is EnqueueOutcome.QUEUED
    assert pipe.enqueue("second") is EnqueueOutcome.DROPPED
    assert pipe.try_enqueue("third") is False

    snapshot = pipe.stats.snapshot()
    assert snapshot["enqueued"] == 1
    assert snapshot["dropped"] == 2
    assert snapshot["buffered"] == 0
    assert snapshot["emitted"] == 0
    dropped = metrics.snapshot()["counter_vecs"]["events_dropped_total"]
    assert dropped["queue_full"] == 2


def test_overflow_with_buffer_reports_buffered_not_dropped_or_emitted() -> None:
    metrics = MetricsCollector()
    buffer = MemoryOfflineBuffer(10)
    sink = _RecordingSink()
    pipe = Pipeline([sink], queue_size=1, offline_buffer=buffer, metrics=metrics)

    assert pipe.enqueue("first") is EnqueueOutcome.QUEUED
    assert pipe.enqueue("second") is EnqueueOutcome.BUFFERED
    assert pipe.try_enqueue("third") is False

    snapshot = pipe.stats.snapshot()
    assert snapshot["enqueued"] == 1
    assert snapshot["buffered"] == 2
    assert snapshot["dropped"] == 0
    assert snapshot["emitted"] == 0
    assert buffer.peek() == ["second", "third"]
    metrics_snapshot = metrics.snapshot()
    assert metrics_snapshot["counters"]["backpressure_total"] == 2
    assert "queue_full" not in metrics_snapshot["counter_vecs"].get("events_dropped_total", {})

    # Buffered events are emitted only once the queue drains / replay runs.
    pipe.close(timeout=1)
    assert sorted(sink.events) == ["first", "second", "third"]
    assert pipe.stats.snapshot()["emitted"] == 3
    assert len(buffer) == 0


@pytest.mark.parametrize("logger_cls", [ClientLogger, EventLogger])
def test_emit_does_not_mark_buffered_overflow_event_as_emitted(
    logger_cls, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Keep workers out of the way so the queue fills deterministically.
    monkeypatch.setattr(pipeline_module.Pipeline, "start", lambda self, workers=1: None)
    metrics = MetricsCollector()
    config = (
        loza.Config.test("overflow")
        .with_sink(loza.MemorySink())
        .with_async(True)
        .with_metrics(metrics)
    )
    config = replace(config, async_config=replace(config.async_config, queue_size=1))
    logger = logger_cls(config)
    assert logger._pipeline is not None

    first = logger.start_event(loza.Params(event="first"))
    logger.finish(first, "success")
    assert logger.emit(first)
    assert first.emitted is True

    second = logger.start_event(loza.Params(event="second"))
    logger.finish(second, "success")
    assert logger.emit(second) == ""
    assert second.emitted is False
    assert second.emitted_payload == ""

    snapshot = logger._pipeline.stats.snapshot()
    assert snapshot["enqueued"] == 1
    assert snapshot["buffered"] == 1
    assert snapshot["dropped"] == 0
    assert snapshot["emitted"] == 0
    assert len(logger._pipeline.offline_buffer) == 1
    assert metrics.snapshot()["counters"]["backpressure_total"] == 1
    logger.shutdown()
