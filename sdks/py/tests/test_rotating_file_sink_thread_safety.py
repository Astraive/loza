"""Regression tests for thread-safe writes in RotatingFileSink (issue #336)."""

from __future__ import annotations

import threading
from pathlib import Path

import pytest

from loza.core import standard_sinks


class _GatedFile:
    """Wraps the sink file so a test can pause it at a chosen operation."""

    def __init__(self, inner: object, operation: str, entered: threading.Event, proceed: threading.Event) -> None:
        self._inner = inner
        self._operation = operation
        self._entered = entered
        self._proceed = proceed

    def write(self, line: str) -> None:
        if self._operation == "write":
            self._gate()
        self._inner.write(line)

    def flush(self) -> None:
        self._inner.flush()

    def close(self) -> None:
        if self._operation == "close":
            self._gate()
        self._inner.close()

    def _gate(self) -> None:
        self._entered.set()
        assert self._proceed.wait(timeout=5), "test gate was never released"


def test_write_holds_lock_across_critical_section(tmp_path: Path) -> None:
    """A concurrent write must not enter while another write is in progress."""
    sink = standard_sinks.RotatingFileSink(str(tmp_path / "rotate.log"), max_bytes=100_000)
    entered = threading.Event()
    proceed = threading.Event()
    sink._file = _GatedFile(sink._file, "write", entered, proceed)

    outcome: dict[str, object] = {}

    def worker() -> None:
        try:
            sink.write("hello")
            outcome["ok"] = True
        except BaseException as exc:  # pragma: no cover - surfaced by the assert below
            outcome["exc"] = exc

    thread = threading.Thread(target=worker)
    thread.start()
    assert entered.wait(timeout=5), "worker never reached the write() critical section"

    # The worker is paused inside write(); no other thread may hold the lock.
    assert not sink._lock.acquire(blocking=False), "write() must hold the sink lock for its whole body"

    proceed.set()
    thread.join(timeout=5)
    assert outcome.get("ok"), outcome.get("exc")
    assert sink._lock.acquire(blocking=False), "write() must release the sink lock when done"
    sink._lock.release()
    sink.close()


def test_rotate_holds_lock_across_critical_section(tmp_path: Path) -> None:
    """A rotation must not race with another write or rotation."""
    sink = standard_sinks.RotatingFileSink(str(tmp_path / "rotate.log"), max_bytes=1, max_backups=3)
    entered = threading.Event()
    proceed = threading.Event()
    # Gate close() so the worker pauses inside _rotate() (size 2 > max_bytes 1).
    sink._file = _GatedFile(sink._file, "close", entered, proceed)

    outcome: dict[str, object] = {}

    def worker() -> None:
        try:
            sink.write("x")
            outcome["ok"] = True
        except BaseException as exc:  # pragma: no cover - surfaced by the assert below
            outcome["exc"] = exc

    thread = threading.Thread(target=worker)
    thread.start()
    assert entered.wait(timeout=5), "worker never reached _rotate()"

    assert not sink._lock.acquire(blocking=False), "_rotate() must hold the sink lock for its whole body"

    proceed.set()
    thread.join(timeout=5)
    assert outcome.get("ok"), outcome.get("exc")
    sink.close()


def test_concurrent_writes_preserve_all_lines(tmp_path: Path) -> None:
    """Concurrent writers must not lose, duplicate, or corrupt lines."""
    path = str(tmp_path / "rotate.log")
    sink = standard_sinks.RotatingFileSink(path, max_bytes=200, max_backups=50)
    messages = [f"message-{i:04d}" for i in range(200)]

    def worker(offset: int) -> None:
        for message in messages[offset::4]:
            sink.write(message)

    threads = [threading.Thread(target=worker, args=(offset,)) for offset in range(4)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join(timeout=15)
    sink.close()

    all_lines: list[str] = []
    for candidate in sorted(tmp_path.glob("rotate.log*")):
        all_lines.extend(candidate.read_text(encoding="utf-8").splitlines())
    assert sorted(all_lines) == sorted(messages)


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit(pytest.main([__file__]))
