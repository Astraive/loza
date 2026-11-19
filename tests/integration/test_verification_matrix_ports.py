#!/usr/bin/env python3
"""Regression tests for TOCTOU-safe port selection in the verification matrix.

``_find_free_port`` can only return a *candidate* port because it closes its
probe socket before the collector binds its own listener. These tests pin the
bind-conflict detection and the retry logic that closes the race window.
"""

from __future__ import annotations

import importlib.util
import socket
import sys
from pathlib import Path

import pytest

_MODULE_PATH = Path(__file__).with_name("verification_matrix.py")
_SPEC = importlib.util.spec_from_file_location("verification_matrix", _MODULE_PATH)
assert _SPEC is not None and _SPEC.loader is not None
vm = importlib.util.module_from_spec(_SPEC)
sys.modules[_SPEC.name] = vm
_SPEC.loader.exec_module(vm)


class _FakeProc:
    """Minimal subprocess.Popen stand-in with a configurable exit state."""

    def __init__(self, exit_code: int | None, args: list[str]) -> None:
        self._exit_code = exit_code
        self.args = args
        self.terminated = False

    def poll(self) -> int | None:
        return self._exit_code

    def terminate(self) -> None:
        self.terminated = True

    def wait(self, timeout: float | None = None) -> int:
        return self._exit_code if self._exit_code is not None else 0


def _conflict_proc(exit_code: int = 1) -> _FakeProc:
    return _FakeProc(exit_code, [])


def test_find_free_port_returns_currently_bindable_port() -> None:
    port = vm._find_free_port()
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
        probe.bind(("127.0.0.1", port))
        assert probe.getsockname()[1] == port


def test_exited_on_bind_conflict_detects_address_in_use(tmp_path: Path) -> None:
    stderr = tmp_path / "stderr.log"
    stdout = tmp_path / "stdout.log"
    stderr.write_text(
        '{"level":"fatal","error":"listen tcp 127.0.0.1:1234: bind: address already in use"}',
        encoding="utf-8",
    )
    stdout.write_text("", encoding="utf-8")
    assert vm._exited_on_bind_conflict(_conflict_proc(), stdout, stderr, grace_s=0.01) is True


def test_exited_on_bind_conflict_ignores_unrelated_exit(tmp_path: Path) -> None:
    stderr = tmp_path / "stderr.log"
    stdout = tmp_path / "stdout.log"
    stderr.write_text("collector: invalid configuration", encoding="utf-8")
    stdout.write_text("", encoding="utf-8")
    assert vm._exited_on_bind_conflict(_conflict_proc(), stdout, stderr, grace_s=0.01) is False


def test_exited_on_bind_conflict_ignores_running_process(tmp_path: Path) -> None:
    stderr = tmp_path / "stderr.log"
    stdout = tmp_path / "stdout.log"
    stderr.write_text("", encoding="utf-8")
    stdout.write_text("", encoding="utf-8")
    running = _FakeProc(None, [])
    assert vm._exited_on_bind_conflict(running, stdout, stderr, grace_s=0.01) is False


def test_start_collector_retries_with_fresh_port_on_bind_conflict(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    first_port, replacement_port = 41001, 41002
    launched_ports: list[int] = []

    monkeypatch.setattr(vm, "_tool_available", lambda name: True)
    monkeypatch.setattr(
        vm,
        "_run",
        lambda *args, **kwargs: vm.StepResult(id="collector.build", category="collector_runtime", status="implemented_and_passing"),
    )
    monkeypatch.setattr(vm, "_find_free_port", lambda: replacement_port)
    monkeypatch.setattr(vm, "_PORT_BIND_GRACE_S", 0.01)

    def fake_popen(command: list[str], **kwargs: object) -> _FakeProc:
        port = int(command[-1].rsplit(":", 1)[1])
        launched_ports.append(port)
        stderr = kwargs["stderr"]
        if port == first_port:
            # Simulate the TOCTOU race: another process took the probed port.
            stderr.write("listen tcp 127.0.0.1:41001: bind: address already in use\n")
            stderr.flush()
            return _FakeProc(1, command)
        return _FakeProc(None, command)

    monkeypatch.setattr(vm.subprocess, "Popen", fake_popen)

    proc, results, runtime = vm._start_collector(tmp_path, first_port)

    assert launched_ports == [first_port, replacement_port]
    assert proc is not None and proc.poll() is None
    assert runtime is not None
    assert runtime.base_url == f"http://127.0.0.1:{replacement_port}"
    assert all(result.status != "implemented_and_failing" for result in results)


def test_start_collector_gives_up_after_exhausting_bind_attempts(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(vm, "_tool_available", lambda name: True)
    monkeypatch.setattr(
        vm,
        "_run",
        lambda *args, **kwargs: vm.StepResult(id="collector.build", category="collector_runtime", status="implemented_and_passing"),
    )
    monkeypatch.setattr(vm, "_find_free_port", lambda: 41099)
    monkeypatch.setattr(vm, "_PORT_BIND_GRACE_S", 0.01)

    attempts: list[int] = []

    def always_conflict(command: list[str], **kwargs: object) -> _FakeProc:
        attempts.append(int(command[-1].rsplit(":", 1)[1]))
        kwargs["stderr"].write("bind: address already in use\n")
        kwargs["stderr"].flush()
        return _FakeProc(1, command)

    monkeypatch.setattr(vm.subprocess, "Popen", always_conflict)

    proc, results, runtime = vm._start_collector(tmp_path, 41001, bind_attempts=3)

    assert len(attempts) == 3
    assert proc is None and runtime is None
    assert any(result.status == "implemented_and_failing" for result in results)
