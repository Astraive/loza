from __future__ import annotations

from loza.core import metric
from loza.core.pipeline import MemoryOfflineBuffer, Pipeline


def test_no_sinks_are_reported_failed_not_emitted() -> None:
    errors: list[Exception] = []
    pipeline = Pipeline([], error_handler=errors.append)

    assert pipeline._write_batch(["one", "two"]) is False

    stats = pipeline.stats.snapshot()
    assert stats["emitted"] == 0
    assert stats["failed"] == 2
    assert stats["dropped"] == 0
    assert stats["batches"] == 1
    assert "no sinks configured" in stats["last_error"]
    assert len(errors) == 1
    assert "no sinks configured" in str(errors[0])


def test_no_sinks_write_sync_and_drain_do_not_report_success() -> None:
    pipeline = Pipeline([])

    pipeline.write_sync("sync-event")
    assert pipeline.try_enqueue("queued-event")
    assert pipeline.drain_once() == 1

    stats = pipeline.stats.snapshot()
    assert stats["emitted"] == 0
    assert stats["failed"] == 2
    assert stats["batches"] == 2


def test_no_sinks_are_counted_as_dropped_in_metrics() -> None:
    metrics = metric.MetricsCollector()
    pipeline = Pipeline([], metrics=metrics)

    pipeline.write_sync("event")

    drop_reasons = metrics.snapshot()["counter_vecs"]["events_dropped_total"]
    assert drop_reasons["no_sinks"] == 1
    assert "transport" not in drop_reasons


def test_no_sinks_offline_replay_acknowledges_nothing() -> None:
    buffer = MemoryOfflineBuffer(10)
    buffer.append("buffered-event")
    pipeline = Pipeline([], offline_buffer=buffer)

    assert pipeline.replay_offline() == 0
    assert len(buffer) == 1
    assert pipeline.stats.snapshot()["emitted"] == 0
