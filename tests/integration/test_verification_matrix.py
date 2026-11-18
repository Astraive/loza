#!/usr/bin/env python3
"""Regression tests for the SDK verification matrix.

Covers two independent fixes:
  * MASS-0862: every SDK method family must be verified by its own distinct
    behavioral test rather than a single blanket ``sdk.<sdk>.categories``
    result (which falsely marked all families as verified).
  * issue #952: a missing ``node`` binary must gate only the JavaScript steps
    in ``run_shared_sdk_conformance`` and not short-circuit the Go/Python/Rust
    collector e2e flows that do not need node.
"""
from __future__ import annotations

import importlib.util
import json
import sys
from pathlib import Path
from unittest.mock import patch

import pytest

_MODULE_PATH = Path(__file__).with_name("verification_matrix.py")
_spec = importlib.util.spec_from_file_location("verification_matrix", _MODULE_PATH)
vm = importlib.util.module_from_spec(_spec)
sys.modules[_spec.name] = vm
assert _spec.loader is not None
_spec.loader.exec_module(vm)


# --- MASS-0862: SDK method-family verification -------------------------------

def _passing(step_id: str) -> vm.StepResult:
    return vm.StepResult(id=step_id, category="sdk_categories", status="implemented_and_passing")


def _families() -> list[str]:
    return list(json.loads(vm.MANIFEST_PATH.read_text(encoding="utf-8"))["method_catalog"])


def test_blanket_category_result_does_not_verify_any_family() -> None:
    """A passing sdk.go.categories result must not verify all families."""
    blanket = _passing("sdk.go.categories")
    result_map = {blanket.id: blanket}
    for family in _families():
        status = vm._family_status_from_results(result_map, "go", family)
        assert status["status"] == "documented_but_not_behaviorally_verified", family


def test_family_is_verified_only_by_its_own_step() -> None:
    family = "client_creation_and_configuration"
    own_id = vm._family_step_id("go", family)
    assert vm._family_status_from_results({own_id: _passing(own_id)}, "go", family) == {
        "status": "implemented_and_passing",
        "evidence": own_id,
    }

    # Another family's passing result must not leak into this one.
    other_id = vm._family_step_id("go", "lifecycle")
    leaked = vm._family_status_from_results({other_id: _passing(other_id)}, "go", family)
    assert leaked["status"] == "documented_but_not_behaviorally_verified"


def test_family_without_a_test_is_never_verified() -> None:
    family = "http_and_framework_helpers"
    assert family not in vm._FAMILY_TEST_SELECTORS  # no SDK covers it behaviorally
    # Even a fabricated passing result for an untested family cannot verify it.
    fake_id = vm._family_step_id("go", family)
    status = vm._family_status_from_results({fake_id: _passing(fake_id)}, "go", family)
    assert status["status"] == "documented_but_not_behaviorally_verified"


def test_environment_blocked_and_failing_propagate() -> None:
    family = "lifecycle"
    own_id = vm._family_step_id("go", family)
    blocked = vm._blocked(own_id, "sdk_categories", "go is unavailable")
    assert vm._family_status_from_results({own_id: blocked}, "go", family) == {
        "status": "environment_blocked",
        "reason": "go is unavailable",
    }

    failing_id = vm._family_step_id("python", family)  # alias python -> py
    failing = vm.StepResult(id=failing_id, category="sdk_categories", status="implemented_and_failing")
    assert vm._family_status_from_results({failing_id: failing}, "python", family) == {
        "status": "implemented_and_failing",
        "evidence": failing_id,
    }


def test_category_suites_emit_distinct_step_ids_per_family(monkeypatch: pytest.MonkeyPatch) -> None:
    def fake_run(step_id: str, category: str, command: list[str], cwd: Path, **_: object) -> vm.StepResult:
        return vm.StepResult(id=step_id, category=category, status="implemented_and_passing", command=command)

    monkeypatch.setattr(vm, "_run", fake_run)
    monkeypatch.setattr(vm, "_tool_available", lambda _name: True)

    results = vm.run_sdk_category_suites()
    ids = [result.id for result in results]
    assert ids, "expected per-family category steps"
    assert len(ids) == len(set(ids)), "category step ids must be unique"

    # No blanket sdk.<sdk>.categories id survives.
    assert not any(step_id.endswith(".categories") for step_id in ids)

    # Every emitted id maps to a declared selector for that SDK/family.
    for step_id in ids:
        parts = step_id.split(".")
        assert parts[0] == "sdk" and parts[2] == "family", step_id
        sdk, family = parts[1], ".".join(parts[3:])
        assert sdk in vm._FAMILY_TEST_SELECTORS[family], step_id
        # The untested family never produces a step.
        assert family != "http_and_framework_helpers"


def test_build_matrix_leaves_untested_family_unverified() -> None:
    manifest = json.loads(vm.MANIFEST_PATH.read_text(encoding="utf-8"))
    matrix = vm.build_matrix([])
    family_matrix = matrix["sdk_method_families"]
    for sdk in manifest["sdks"]:
        assert family_matrix[sdk]["http_and_framework_helpers"]["status"] == (
            "documented_but_not_behaviorally_verified"
        )


# --- issue #952: node gating in run_shared_sdk_conformance -------------------

def _collector() -> vm.CollectorRuntime:
    return vm.CollectorRuntime(
        base_url="http://127.0.0.1:19999",
        ingest_token="ingest-token",
        admin_token="admin-token",
    )


def _recording_run(calls: list[list[str]]):
    def fake_run(*args, **kwargs) -> vm.StepResult:
        step_id, category, command = args[0], args[1], list(args[2])
        calls.append(command)
        return vm.StepResult(
            id=step_id,
            category=category,
            status="implemented_and_passing",
            command=command,
        )

    return fake_run


def _available_except(missing: set[str]):
    return lambda name: name not in missing


def test_missing_node_does_not_block_go_python_rust_e2e():
    """A missing node gates only the JS steps; the other SDK e2e flows still run."""
    calls: list[list[str]] = []
    with (
        patch.object(vm, "_tool_available", side_effect=_available_except({"node"})),
        patch.object(vm, "_start_collector", return_value=(object(), [], _collector())),
        patch.object(vm, "_wait_for_http", return_value=True),
        patch.object(vm, "_run", side_effect=_recording_run(calls)),
        patch.object(vm, "_stop_process"),
    ):
        results = vm.run_shared_sdk_conformance()

    by_id = {result.id: result for result in results}

    # Regression assertion: these must run even though node is unavailable.
    for step_id in ("sdk.go.collector_e2e", "sdk.py.collector_e2e", "sdk.rs.collector_e2e"):
        assert step_id in by_id, f"{step_id} was skipped when node is unavailable"
        assert by_id[step_id].status == "implemented_and_passing"

    # Only the node/JS-dependent steps are gated.
    assert by_id["sdk.js.collector_e2e"].status == "environment_blocked"
    assert "node" in by_id["sdk.js.collector_e2e"].details["reason"]
    assert by_id["sdk.shared_conformance.javascript"].status == "environment_blocked"

    # Non-JS shared conformance checks are preserved.
    for step_id in (
        "sdk.shared_conformance.go",
        "sdk.shared_conformance.python",
        "sdk.shared_conformance.rust",
    ):
        assert by_id[step_id].status == "implemented_and_passing"

    assert not any(command[0] in {"npm", "npm.cmd"} for command in calls)
    assert any(command[:2] == ["go", "test"] for command in calls)
    assert any(command[:2] == ["cargo", "test"] for command in calls)


def test_node_available_runs_all_e2e_steps():
    """When node is present the previous behavior is unchanged."""
    with (
        patch.object(vm, "_tool_available", side_effect=_available_except(set())),
        patch.object(vm, "_start_collector", return_value=(object(), [], _collector())),
        patch.object(vm, "_wait_for_http", return_value=True),
        patch.object(vm, "_run", side_effect=_recording_run([])),
        patch.object(vm, "_stop_process"),
    ):
        results = vm.run_shared_sdk_conformance()

    by_id = {result.id: result for result in results}
    for step_id in (
        "sdk.shared_conformance",
        "sdk.go.collector_e2e",
        "sdk.js.collector_e2e",
        "sdk.py.collector_e2e",
        "sdk.rs.collector_e2e",
    ):
        assert by_id[step_id].status == "implemented_and_passing"
