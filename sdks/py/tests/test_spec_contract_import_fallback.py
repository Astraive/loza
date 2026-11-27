"""Regression tests for the authoritative-contract import fallback.

The generated compatibility layer must only fall back to the monorepo/backup
contract when the authoritative ``loza_contract`` dependency is genuinely
missing.  Any other import-time failure must propagate so consumers never
silently run against a stale contract that omits spec/security fixes.
"""

from __future__ import annotations

import importlib.util
import logging
import sys
import types
from pathlib import Path

import pytest

SPEC_CONTRACT_PATH = Path(__file__).resolve().parents[1] / "src" / "loza" / "generated" / "spec_contract.py"


def _load_spec_contract(module_name: str):
    """Load a fresh copy of the compatibility layer under an isolated name."""
    spec = importlib.util.spec_from_file_location(module_name, SPEC_CONTRACT_PATH)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class _FailFirstImport:
    """Meta path finder that hides ``loza_contract`` on the first import only."""

    def __init__(self) -> None:
        self.calls = 0

    def find_spec(self, fullname, path=None, target=None):
        if fullname == "loza_contract":
            self.calls += 1
            if self.calls == 1:
                raise ModuleNotFoundError("simulated missing loza_contract", name=fullname)
        return None


def test_non_import_error_from_authoritative_contract_propagates(monkeypatch):
    """A NameError in the real contract must not be masked by the fallback."""
    fake_contract = types.ModuleType("loza_contract")

    def _raise(*_args, **_kwargs):
        raise NameError("authoritative contract failed to initialize")

    fake_contract.__getattr__ = _raise
    monkeypatch.setitem(sys.modules, "loza_contract", fake_contract)

    with pytest.raises(NameError, match="authoritative contract failed to initialize"):
        _load_spec_contract("_mass0683_spec_contract_propagate")


def test_missing_dependency_still_falls_back_and_logs(monkeypatch, caplog):
    """A genuinely missing dependency still uses the monorepo fallback and logs why."""
    monkeypatch.delitem(sys.modules, "loza_contract", raising=False)
    # ``sys.path`` is mutated in-place by the fallback; monkeypatch restores the
    # original list object on teardown.
    monkeypatch.setattr(sys, "path", list(sys.path))
    finder = _FailFirstImport()
    monkeypatch.setattr(sys, "meta_path", [finder, *sys.meta_path])

    with caplog.at_level(logging.WARNING):
        module = _load_spec_contract("_mass0683_spec_contract_fallback")

    assert module.LOZA_SPEC_VERSION
    assert finder.calls == 2
    assert any("falling back" in rec.getMessage().lower() for rec in caplog.records)
