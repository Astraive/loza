"""Regression tests for the codegen/manifest source-of-truth boundary.

The canonical fixture manifest at spec/conformance/manifest.json is a
hand-maintained *input* to the generator (see model.build_contract).  The
generator must only emit to generated/ paths and must never overwrite that
input, otherwise regeneration destroys direct edits and --check ends up
comparing a file against a transformation of itself.
"""

from __future__ import annotations

import json
import shutil
import sys
from pathlib import Path

import pytest

SPEC_ROOT = Path(__file__).resolve().parents[1]
CODEGEN_DIR = SPEC_ROOT / "codegen"
if str(CODEGEN_DIR) not in sys.path:
    sys.path.insert(0, str(CODEGEN_DIR))

import generate  # noqa: E402

SENTINEL_KEY = "__source_sentinel__"
SENTINEL_VALUE = "do-not-overwrite"
SCHEMA_FILES = (
    "event.schema.json",
    "event.strict.schema.json",
    "event.loose.schema.json",
    "ingest.schema.json",
    "collector-response.schema.json",
)


@pytest.fixture()
def spec_root(tmp_path: Path) -> Path:
    """Build a minimal spec tree with a marked canonical manifest."""
    root = tmp_path / "spec"
    (root / "schemas" / "json").mkdir(parents=True)
    (root / "conformance").mkdir(parents=True)

    for name in SCHEMA_FILES:
        shutil.copyfile(SPEC_ROOT / "schemas" / "json" / name, root / "schemas" / "json" / name)
    shutil.copyfile(SPEC_ROOT / "loza-spec.yaml", root / "loza-spec.yaml")

    manifest = json.loads((SPEC_ROOT / "conformance" / "manifest.json").read_text(encoding="utf-8"))
    manifest[SENTINEL_KEY] = SENTINEL_VALUE
    (root / "conformance" / "manifest.json").write_text(
        json.dumps(manifest, indent=2) + "\n", encoding="utf-8"
    )
    return root


def _run(monkeypatch: pytest.MonkeyPatch, root: Path, *extra: str) -> int:
    monkeypatch.setattr(sys, "argv", ["generate.py", "--spec-root", str(root), *extra])
    return generate.main()


def test_generator_does_not_overwrite_canonical_input(spec_root: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    canonical = spec_root / "conformance" / "manifest.json"
    original = canonical.read_text(encoding="utf-8")

    assert _run(monkeypatch, spec_root) == 0

    assert canonical.read_text(encoding="utf-8") == original
    assert SENTINEL_VALUE in canonical.read_text(encoding="utf-8")


def test_generator_emits_manifest_only_to_generated_paths(
    spec_root: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    assert _run(monkeypatch, spec_root) == 0

    for relative in (
        "generated/conformance_manifest.json",
        "generated/contract/conformance_manifest.json",
    ):
        artifact = spec_root / relative
        assert artifact.is_file(), f"missing generated artifact: {relative}"
        assert SENTINEL_VALUE not in artifact.read_text(encoding="utf-8")


def test_check_does_not_validate_against_source_manifest(
    spec_root: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Generate once so all generated/ outputs are current.
    assert _run(monkeypatch, spec_root) == 0

    # A direct edit to the source manifest must not make --check fail: the
    # source is an input, not a tool-maintained output. product_version is a
    # field the model always derives from loza-spec.yaml, so editing it here
    # cannot legitimately stale any generated/ artifact.
    canonical = spec_root / "conformance" / "manifest.json"
    payload = json.loads(canonical.read_text(encoding="utf-8"))
    payload["product_version"] = "0.0.0-hand-edited"
    canonical.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")

    assert _run(monkeypatch, spec_root, "--check") == 0
