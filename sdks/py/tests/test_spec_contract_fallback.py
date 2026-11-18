from __future__ import annotations

import importlib.machinery
import importlib.util
import sys
import textwrap
from pathlib import Path
from typing import Any

import pytest

# The compatibility layer under test.
_MODULE_PATH = Path(__file__).resolve().parents[1] / "src" / "loza" / "generated" / "spec_contract.py"
_BACKUP_LOADER_NAME = "loza_generated_spec_contract_backup"

_MISSING = object()

# Minimal backup contract that exports every symbol spec_contract.py reads from a
# backup EXCEPT validate_flexible_json_bytes -- the exact condition from MASS-0684.
_BACKUP_WITHOUT_VALIDATOR = textwrap.dedent(
    """
    LOZA_SPEC_VERSION = "v1"
    LOZA_INGEST_API_VERSION = "v1"
    LOZA_EVENT_VERSION = "v1"
    ALLOWED_TOP_LEVEL_FIELDS = frozenset()
    CANONICAL_FIELDS = frozenset()
    ALLOWED_KINDS = frozenset()
    ALLOWED_LEVELS = frozenset()
    ALLOWED_OUTCOMES = frozenset()
    ALLOWED_PARTIAL_REASONS = frozenset()
    ALLOWED_EVENT_STATES = frozenset()

    class CollectorAck: pass
    class CollectorError(Exception): pass
    class CollectorResponse: pass

    def parse_collector_response(*args, **kwargs): pass
    def looks_like_loza_event(*args, **kwargs): pass
    def normalize_event_aliases(*args, **kwargs): pass
    def build_ingest_envelope(*args, **kwargs): pass
    def validate_event_payload(*args, **kwargs): pass
    # NOTE: validate_flexible_json_bytes deliberately absent.
    """
)


def _load_via_backup(tmp_path: Path, backup_source: str) -> Any:
    """Load a fresh copy of spec_contract.py, forcing its backup-import path."""
    module_copy = tmp_path / "spec_contract.py"
    module_copy.write_text(_MODULE_PATH.read_text())
    (tmp_path / "spec_contract.py.bak").write_text(backup_source)

    # Force the primary `from loza_contract import ...` to fail so the module
    # falls through to loading the .bak file. A None entry in sys.modules makes
    # the import raise ModuleNotFoundError without hitting the filesystem.
    saved_contract = sys.modules.get("loza_contract", _MISSING)
    sys.modules["loza_contract"] = None
    sys.modules.pop(_BACKUP_LOADER_NAME, None)
    try:
        loader = importlib.machinery.SourceFileLoader("_test_spec_contract_under_test", str(module_copy))
        spec = importlib.util.spec_from_loader(loader.name, loader)
        module = importlib.util.module_from_spec(spec)
        loader.exec_module(module)
        return module
    finally:
        if saved_contract is _MISSING:
            sys.modules.pop("loza_contract", None)
        else:
            sys.modules["loza_contract"] = saved_contract
        sys.modules.pop(_BACKUP_LOADER_NAME, None)


def test_backup_without_validator_still_exports_callable(tmp_path: Path) -> None:
    """MASS-0684: missing backup symbol must not leave an uncallable None export."""
    module = _load_via_backup(tmp_path, _BACKUP_WITHOUT_VALIDATOR)

    assert "validate_flexible_json_bytes" in module.__all__
    assert module.validate_flexible_json_bytes is not None
    assert callable(module.validate_flexible_json_bytes)
    with pytest.raises(NotImplementedError):
        module.validate_flexible_json_bytes(b"{}", False)


def test_backup_with_validator_uses_real_implementation(tmp_path: Path) -> None:
    """The fallback must not shadow a real implementation when the backup has one."""
    backup_source = _BACKUP_WITHOUT_VALIDATOR + textwrap.dedent(
        """
        def validate_flexible_json_bytes(raw, strict):
            return (raw, strict)
        """
    )
    module = _load_via_backup(tmp_path, backup_source)

    assert module.validate_flexible_json_bytes(b"payload", True) == (b"payload", True)
