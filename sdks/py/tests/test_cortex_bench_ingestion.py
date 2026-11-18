"""Regression tests for ingestion throughput batching (issue #776)."""

from types import SimpleNamespace

from bench import cortex_bench


class _FakeEngine:
    """Captures ingested batches without any network I/O."""

    instances: list["_FakeEngine"] = []

    def __init__(self, cortex_url: str = "") -> None:
        self.cortex_url = cortex_url
        self.batches: list[list[dict]] = []
        _FakeEngine.instances.append(self)

    def ingest(self, events) -> None:
        self.batches.append(list(events))

    def reconstruct_context(self, signal, mode: str = "fast"):
        return SimpleNamespace(
            causal_chain=[object()],
            similar_past_incidents=[object()],
            suggested_remediations=[object()],
            confidence=0.5,
            explain="fake explanation",
        )


def _dataset(n_eval: int) -> dict:
    return {
        "train_events": [{"kind": "train"}],
        "eval_events": [{"n": i} for i in range(n_eval)],
        "eval_signals": [{"incident_id": f"INC-{i}"} for i in range(10)],
    }


def _run_with_dataset(monkeypatch, dataset: dict):
    """Run the benchmark against a fake engine and deterministic clock."""
    _FakeEngine.instances.clear()
    ticks = iter(range(1, 1_000_000))
    monkeypatch.setattr(cortex_bench.time, "monotonic", lambda: next(ticks) / 1000.0)
    monkeypatch.setattr(cortex_bench, "_wait_for_cortex", lambda *a, **kw: None)
    monkeypatch.setattr(cortex_bench, "generate_dataset", lambda **kw: dataset)
    monkeypatch.setattr(cortex_bench, "Engine", _FakeEngine)
    results = cortex_bench.run_benchmark()
    return results, _FakeEngine.instances[-1]


def test_ingestion_throughput_uses_distinct_batches(monkeypatch):
    """Each timed ingest must consume a unique slice of eval_events.

    Before the fix, all 10 timed ingests re-used the first 1000 events, so the
    rest of the eval set was never measured and a deduplicating engine would
    report inflated throughput.
    """
    # 2 full batches (1000 each) plus a short final batch (25).
    dataset = _dataset(2025)
    _, engine = _run_with_dataset(monkeypatch, dataset)

    # First ingest is cold start on train; the rest are throughput batches.
    throughput_batches = engine.batches[1:]
    assert len(throughput_batches) == 3
    assert [len(batch) for batch in throughput_batches] == [1000, 1000, 25]

    # Union of the batches is exactly the eval set, in order, with no repeats.
    ingested = [event for batch in throughput_batches for event in batch]
    assert ingested == dataset["eval_events"]
    assert len(ingested) == len(set(id(e) for e in ingested))


def test_ingestion_throughput_stops_when_events_run_out(monkeypatch):
    """Fewer events than n_batches*batch_size must not add empty timed batches."""
    dataset = _dataset(1500)
    _, engine = _run_with_dataset(monkeypatch, dataset)

    throughput_batches = engine.batches[1:]
    assert [len(batch) for batch in throughput_batches] == [1000, 500]
    assert all(throughput_batches)  # no empty/no-op ingests were timed
