"""Regression tests for the stale-artifact remediation message in codegen/generate.py."""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

CODEGEN_DIR = Path(__file__).resolve().parents[1] / "codegen"
if str(CODEGEN_DIR) not in sys.path:
    sys.path.insert(0, str(CODEGEN_DIR))

import generate  # noqa: E402

STUBS = (
    "build_contract",
    "build_cortex_contract",
    "render_conformance_manifest",
    "render_contract_json",
    "render_cortex_contract_json",
    "render_go_contract",
    "render_python_contract",
    "render_rust_contract",
)


@pytest.mark.parametrize(
    "invoked_as, expected_command",
    [
        # Reproduces the reported defect: run from the repository root.
        ("spec/codegen/generate.py", "python spec/codegen/generate.py"),
        # Documented working directory (CI and RELEASE_PROCESS.md) must keep working.
        ("codegen/generate.py", "python codegen/generate.py"),
    ],
)
def test_stale_artifact_message_uses_invocation_path(
    monkeypatch, capsys, tmp_path, invoked_as, expected_command
):
    """The remediation command must be runnable from the caller's working directory."""
    for name in STUBS:
        monkeypatch.setattr(generate, name, lambda *args, **kwargs: "artifact")

    # Point at an empty spec root so every expected artifact is missing and the
    # stale-artifact branch is exercised deterministically.
    monkeypatch.setattr(sys, "argv", [invoked_as, "--check", "--spec-root", str(tmp_path)])

    exit_code = generate.main()
    output = capsys.readouterr().out

    assert exit_code == 1
    assert "generated artifact(s) are out of date" in output
    assert f"Run: {expected_command}" in output
