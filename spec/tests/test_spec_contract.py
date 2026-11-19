"""Tests for the machine-readable spec contract (LOZA-DOCS-002)."""

from __future__ import annotations

import importlib.util
import json
from pathlib import Path

import pytest

SPEC_ROOT = Path(__file__).resolve().parents[1]

_spec = importlib.util.spec_from_file_location(
    "check_spec_contract", SPEC_ROOT / "scripts" / "check_spec_contract.py"
)
assert _spec is not None and _spec.loader is not None
checker = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(checker)


def _load_json(path: Path) -> object:
    with path.open("r", encoding="utf-8") as handle:
        return json.load(handle)


def test_all_schema_and_openapi_refs_resolve() -> None:
    assert checker.check_refs() == []


def test_readme_example_validates_against_canonical_schema() -> None:
    assert checker.check_readme_example() == []


def test_single_auth_mechanism_is_documented() -> None:
    assert checker.check_auth_docs() == []


def test_registry_metadata_matches_generated_artifacts() -> None:
    assert checker.check_registry_integrity() == []


def test_conformance_uses_published_strict_schema() -> None:
    assert checker.check_strict_schema_contract() == []


def test_published_strict_schema_enforces_strictness() -> None:
    """With jsonschema available, verify the published definition really works."""
    pytest.importorskip("jsonschema")
    from jsonschema import Draft202012Validator

    strict = _load_json(SPEC_ROOT / "schemas" / "json" / "event.strict.schema.json")
    assert isinstance(strict, dict)
    validator = Draft202012Validator(strict)

    alias = _load_json(SPEC_ROOT / "fixtures" / "invalid" / "strict_event_type_alias.json")
    loose_alias = _load_json(SPEC_ROOT / "fixtures" / "valid" / "loose_event_type_alias.json")
    minimal = _load_json(SPEC_ROOT / "fixtures" / "valid" / "minimal_event.json")
    http_success = _load_json(SPEC_ROOT / "fixtures" / "valid" / "http_success.json")
    assert list(validator.iter_errors(alias)), "strict schema must reject the legacy alias"
    assert list(validator.iter_errors(loose_alias)), "strict schema must reject the loose-only alias"
    assert not list(validator.iter_errors(minimal)), "strict schema must accept canonical events"
    assert not list(validator.iter_errors(http_success)), "strict schema must accept all canonical fields"
