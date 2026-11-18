"""Regression test: benchmarks must emit through their configured logger.

The module-level ``loza.*`` facade functions delegate to a separate global
default logger. A benchmark that creates a configured logger with ``loza.new``
but then calls the module-level ``finish``/``emit`` silently bypasses its own
MemorySink and writes to that global default instead.
"""

from __future__ import annotations

import sys
from pathlib import Path

import loza
import pytest

# The bench package lives next to tests/ and is not installed; make it
# importable regardless of pytest's import mode.
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from bench.emit_bench import run_emit_benchmark, run_emit_enriched_benchmark  # noqa: E402
from bench.encoder_bench import run_encoder_benchmark  # noqa: E402
from bench.middleware_bench import (  # noqa: E402
    run_middleware_simulation_benchmark,
    run_plain_emit_benchmark,
)
from bench.sampler_bench import _run_sampler_bench  # noqa: E402

ITERATIONS = 4


def _run_sample_all_bench() -> None:
    _run_sampler_bench("sample_all", loza.SampleAll(), ITERATIONS)


def _capture_sinks(monkeypatch: pytest.MonkeyPatch) -> tuple[loza.MemorySink, list[loza.MemorySink]]:
    """Point the global default at a sentinel sink and track benchmark sinks."""
    real_sink = loza.MemorySink
    global_sink = real_sink()
    global_logger = loza.new(loza.test("global").with_sink(global_sink))
    monkeypatch.setattr(loza, "_default", global_logger)

    created: list[loza.MemorySink] = []

    def tracking_sink(*args: object, **kwargs: object) -> loza.MemorySink:
        sink = real_sink(*args, **kwargs)
        created.append(sink)
        return sink

    monkeypatch.setattr(loza, "MemorySink", tracking_sink)
    return global_sink, created


@pytest.mark.parametrize(
    ("run", "expected_events"),
    [
        (lambda: run_middleware_simulation_benchmark(ITERATIONS), ITERATIONS),
        (lambda: run_plain_emit_benchmark(ITERATIONS), ITERATIONS),
        (lambda: run_emit_benchmark(ITERATIONS), ITERATIONS),
        (lambda: run_emit_enriched_benchmark(ITERATIONS), ITERATIONS),
        (lambda: run_encoder_benchmark(ITERATIONS), 3 * ITERATIONS),
        (_run_sample_all_bench, ITERATIONS),
    ],
)
def test_benchmark_emits_to_its_own_sink_not_global_default(
    monkeypatch: pytest.MonkeyPatch,
    run,
    expected_events: int,
) -> None:
    global_sink, created = _capture_sinks(monkeypatch)

    run()

    # No event may leak to the global default logger's sink.
    assert global_sink.events == []
    # Every event must land in the benchmark's own configured MemorySink.
    assert len(created) == 1
    assert len(created[0].events) == expected_events
