#!/usr/bin/env python3
"""Regression tests for the SDK method-family verification matrix (MASS-0862).

Before the fix, every family was mapped to a single ``sdk.<sdk>.categories``
result, so one passing suite falsely marked all 11 families as
``implemented_and_passing`` -- including families with no behavioral test.
"""
from __future__ import annotations

import importlib.util
import json
import sys
from pathlib import Path

import pytest

_MODULE_PATH = Path(__file__).with_name("verification_matrix.py")
_spec = importlib.util.spec_from_file_location("verification_matrix", _MODULE_PATH)
vm = importlib.util.module_from_spec(_spec)
sys.modules[_spec.name] = vm
assert _spec.loader is not None
_spec.loader.exec_module(vm)


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
