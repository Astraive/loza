from __future__ import annotations

import json
import threading
import time

from loza.core import pipeline


class _YieldingInt(int):
    """An int that releases the GIL between the load and store of ``+=``.

    This makes the historical lost-update race deterministic: if a counter is
    updated without a lock, two threads read the same value and one increment
    disappears. Guarded updates must still produce the exact total.
    """

    def __iadd__(self, other: int) -> int:
        time.sleep(0.001)
        return _YieldingInt(int.__add__(self, other))

    def __add__(self, other: int) -> int:
        time.sleep(0.001)
        return _YieldingInt(int.__add__(self, other))


class _ThreadSafeBatchSink:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self.batches: list[list[str]] = []

    def write_batch(self, encoded_events: list[str]) -> None:
        with self._lock:
            self.batches.append(list(encoded_events))


def test_delivery_stats_counter_updates_are_locked() -> None:
    """Regression for #248: shared stat counters must not lose increments."""
    stats = pipeline.DeliveryStats()
    stats.enqueued = _YieldingInt(0)
    stats.emitted = _YieldingInt(0)
    stats.batches = _YieldingInt(0)

    thread_count, per_thread = 4, 25
    barrier = threading.Barrier(thread_count)

    def worker() -> None:
        barrier.wait()
        for _ in range(per_thread):
            stats.record_enqueued()
            stats.record_emitted(1)
            stats.record_batch()

    threads = [threading.Thread(target=worker) for _ in range(thread_count)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()

    snapshot = stats.snapshot()
    assert snapshot["enqueued"] == thread_count * per_thread
    assert snapshot["emitted"] == thread_count * per_thread
    assert snapshot["batches"] == thread_count * per_thread


def test_pipeline_worker_stats_match_enqueued_events() -> None:
    """Multiple workers updating stats concurrently must report exact totals."""
    sink = _ThreadSafeBatchSink()
    total = 2000
    pipe = pipeline.Pipeline([sink], queue_size=total + 10, max_batch_bytes=128)

    for index in range(total):
        assert pipe.try_enqueue(json.dumps({"i": index}))

    pipe.start(4)
    pipe.close(timeout=10)

    snapshot = pipe.stats.snapshot()
    assert snapshot["enqueued"] == total
    assert snapshot["dropped"] == 0
    assert snapshot["failed"] == 0
    assert snapshot["emitted"] == total
    assert sum(len(batch) for batch in sink.batches) == total
    assert snapshot["batches"] == len(sink.batches)
