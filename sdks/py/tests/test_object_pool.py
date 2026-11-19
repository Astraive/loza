"""Regression tests for the ObjectPool.get() TOCTOU race (MASS-0719).

An ``empty()`` check followed by a blocking ``get()`` is not atomic: two
callers can both observe a non-empty queue and then one blocks forever in
``get()`` instead of falling back to the factory. ``get()`` must use the
non-blocking dequeue so the fallback is atomic with respect to removal.
"""

from __future__ import annotations

import threading
from queue import SimpleQueue

import pytest

from loza.internal.pool.pool import ObjectPool as InternalObjectPool
from loza.pool.pool import ObjectPool

POOL_TYPES = [ObjectPool, InternalObjectPool]


@pytest.mark.parametrize("pool_type", POOL_TYPES)
def test_get_returns_pooled_item(pool_type) -> None:
    pool = pool_type(factory=lambda: "factory")
    pool.put("item")
    assert pool.get() == "item"


@pytest.mark.parametrize("pool_type", POOL_TYPES)
def test_get_falls_back_to_factory_when_empty(pool_type) -> None:
    calls = []
    pool = pool_type(factory=lambda: calls.append(1) or "factory")

    assert pool.get() == "factory"
    assert len(calls) == 1


@pytest.mark.parametrize("pool_type", POOL_TYPES)
def test_get_does_not_hang_on_stale_empty_observation(pool_type) -> None:
    """A stale ``empty()`` observation must never cause a blocking ``get()``."""
    barrier = threading.Barrier(2, timeout=5)

    class RacyQueue(SimpleQueue):
        """Force two callers to both observe ``empty() == False`` first."""

        def __init__(self) -> None:
            super().__init__()
            self.checks = 0

        def empty(self) -> bool:
            self.checks += 1
            if self.checks <= 2:
                barrier.wait()
                return False  # stale: both callers believe an item exists
            return super().empty()

    pool = pool_type(factory=lambda: "factory")
    queue = RacyQueue()
    queue.put("item")
    pool.items = queue

    results = []

    def caller() -> None:
        results.append(pool.get())

    threads = [threading.Thread(target=caller, daemon=True) for _ in range(2)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join(timeout=5)

    assert not any(thread.is_alive() for thread in threads), (
        "get() blocked forever after a stale empty() observation"
    )
    assert sorted(results) == ["factory", "item"]
