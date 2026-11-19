from __future__ import annotations

import subprocess
import sys
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parents[3]
_SPEC_CONTRACT_SRC = _REPO_ROOT / "sdks" / "py" / "src" / "loza" / "generated" / "spec_contract.py"
_CONTRACT_SRC = _REPO_ROOT / "spec" / "generated" / "python" / "loza_contract.py"

# Runs in an isolated interpreter so `loza_contract` is not importable and the
# development fallback branch is exercised. Importing spec_contract must not
# leave the process-wide sys.path modified.
_PROBE = """
import importlib.machinery
import importlib.util
import sys

before = list(sys.path)
loader = importlib.machinery.SourceFileLoader("spec_contract_under_test", sys.argv[1])
spec = importlib.util.spec_from_loader(loader.name, loader)
module = importlib.util.module_from_spec(spec)
sys.modules[loader.name] = module
loader.exec_module(module)
print("PATH_UNCHANGED" if sys.path == before else "PATH_MUTATED")
print("VERSION", module.LOZA_SPEC_VERSION)
print("CALLABLE", callable(module.validate_event_payload))
"""


def test_fallback_import_does_not_mutate_global_sys_path(tmp_path) -> None:
    # Recreate the monorepo layout spec_contract.py expects: the spec directory
    # is six parents up from the spec_contract.py file.
    temp_contract = tmp_path / "spec" / "generated" / "python" / "loza_contract.py"
    temp_contract.parent.mkdir(parents=True)
    temp_contract.write_text(_CONTRACT_SRC.read_text())

    temp_spec_contract = tmp_path / "sdks" / "py" / "src" / "loza" / "generated" / "spec_contract.py"
    temp_spec_contract.parent.mkdir(parents=True)
    temp_spec_contract.write_text(_SPEC_CONTRACT_SRC.read_text())

    result = subprocess.run(
        [sys.executable, "-I", "-S", "-c", _PROBE, str(temp_spec_contract)],
        capture_output=True,
        text=True,
        cwd=tmp_path,
        timeout=60,
    )
    assert result.returncode == 0, result.stderr
    assert "PATH_UNCHANGED" in result.stdout, result.stdout
    # Sanity: the contract symbols really were loaded through the fallback.
    assert "VERSION v1" in result.stdout, result.stdout
    assert "CALLABLE True" in result.stdout, result.stdout
