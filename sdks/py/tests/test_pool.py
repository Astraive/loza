from __future__ import annotations

from queue import Empty

import pytest

from loza.internal.pool.pool import ObjectPool as InternalObjectPool
from loza.pool.pool import ObjectPool as PublicObjectPool


POOL_IMPLEMENTATIONS = [InternalObjectPool, PublicObjectPool]


class _DrainedBetweenCheckAndGet:
    """Queue double that reports an item at check time, then has it drained.

    This deterministically models another thread consuming the only item
    between ``empty()`` and ``get()`` -- the interleave that makes the old
    check-then-act implementation block forever.
    """

    def __init__(self) -> None:
        self.empty_called = False
        self.blocking_get_called = False

    def empty(self) -> bool:
        self.empty_called = True
        return False

    def get(self):  # pragma: no cover - must never be reached after the fix
        self.blocking_get_called = True
        raise AssertionError("blocking get() was used after an empty() check")

    def get_nowait(self):
        raise Empty()

    def put(self, item) -> None:
        pass


@pytest.mark.parametrize("pool_cls", POOL_IMPLEMENTATIONS)
def test_get_does_not_block_when_item_is_drained_after_check(pool_cls):
    pool = pool_cls(factory=lambda: "created")
    drained = _DrainedBetweenCheckAndGet()
    pool.items = drained

    assert pool.get() == "created"
    assert drained.blocking_get_called is False
    assert drained.empty_called is False


@pytest.mark.parametrize("pool_cls", POOL_IMPLEMENTATIONS)
def test_get_returns_pooled_item(pool_cls):
    pool = pool_cls(factory=lambda: "created")
    pool.put("reused")
    assert pool.get() == "reused"


@pytest.mark.parametrize("pool_cls", POOL_IMPLEMENTATIONS)
def test_get_creates_when_empty(pool_cls):
    pool = pool_cls(factory=lambda: "created")
    assert pool.get() == "created"
