from __future__ import annotations

import importlib.util
from pathlib import Path
from types import SimpleNamespace

import pytest

PACKAGE_DIR = Path(__file__).resolve().parents[1]
RUN_COVERAGE = PACKAGE_DIR / "run_coverage.py"


def _load_run_coverage():
    spec = importlib.util.spec_from_file_location("run_coverage", RUN_COVERAGE)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_coverage_gate_is_independent_of_caller_cwd(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    """Running the gate via an absolute path must not depend on the caller's CWD."""
    module = _load_run_coverage()
    calls: list[tuple[list[str], dict[str, object]]] = []

    def fake_run(command: list[str], **kwargs: object) -> SimpleNamespace:
        calls.append((command, kwargs))
        return SimpleNamespace(returncode=0)

    monkeypatch.setattr(module.subprocess, "run", fake_run)
    monkeypatch.chdir(tmp_path)

    assert RUN_COVERAGE.is_absolute() and RUN_COVERAGE.exists()
    assert module.main() == 0
    assert calls

    for command, kwargs in calls:
        assert kwargs["cwd"] == PACKAGE_DIR
        assert kwargs["check"] is False

    run_commands = [command for command, _ in calls if "run" in command]
    assert len(run_commands) == 1
    source_args = [arg for arg in run_commands[0] if arg.startswith("--source=")]
    assert source_args == [f"--source={PACKAGE_DIR / 'src' / 'loza'}"]
    assert Path(source_args[0].split("=", 1)[1]).is_absolute()


def test_coverage_gate_propagates_first_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    module = _load_run_coverage()
    calls: list[list[str]] = []

    def fake_run(command: list[str], **_kwargs: object) -> SimpleNamespace:
        calls.append(command)
        return SimpleNamespace(returncode=7)

    monkeypatch.setattr(module.subprocess, "run", fake_run)

    assert module.main() == 7
    assert len(calls) == 1
