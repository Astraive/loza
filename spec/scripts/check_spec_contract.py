#!/usr/bin/env python3
"""Validate the machine-readable spec contract.

Checks (stdlib only, safe to run offline in CI):

* every local JSON Schema / OpenAPI ``$ref`` resolves to an existing file
* the README example event validates against the canonical event schema
* a single auth mechanism (``Authorization: Bearer``) is documented
* the published registry manifest matches the artifact it describes
* conformance uses the published strict schema instead of a synthesized copy

The frozen ``releases/`` tree is intentionally excluded: those snapshots are
immutable and are not part of the active contract. The active contract is
documented in ``spec/README.md`` and the legacy trees are marked deprecated
there and in ``v1/README.md``.
"""

from __future__ import annotations

import hashlib
import json
import re
import sys
from pathlib import Path
from typing import Any

if sys.stdout.encoding and sys.stdout.encoding.lower() != "utf-8":
    sys.stdout.reconfigure(encoding="utf-8")  # type: ignore[union-attr]
    sys.stderr.reconfigure(encoding="utf-8")  # type: ignore[union-attr]


SPEC_ROOT = Path(__file__).resolve().parents[1]
REPO_ROOT = SPEC_ROOT.parent
CANONICAL_SCHEMA_DIR = SPEC_ROOT / "schemas" / "json"
ARCHIVE_DIR = SPEC_ROOT / "releases"
OPENAPI_FILES = (
    SPEC_ROOT / "openapi" / "collector.openapi.yaml",
)
AUTH_DOCS = (
    SPEC_ROOT / "auth" / "spec.md",
    SPEC_ROOT / "docs" / "SDK_CONFORMANCE_CONTRACT.md",
)

_MISSING = object()
_YAML_REF = re.compile(r"\$ref:\s*['\"]?([^\s'\"]+)")


def _load_json(path: Path) -> Any:
    with path.open("r", encoding="utf-8") as handle:
        return json.load(handle)


def _is_remote(ref: str) -> bool:
    return ref.startswith(("http://", "https://", "urn:"))


def _is_archived(path: Path) -> bool:
    """Frozen release snapshots are immutable and not subject to active checks."""
    return path.is_relative_to(ARCHIVE_DIR)


def _resolve_pointer(document: Any, pointer: str) -> Any:
    if pointer in ("", "#"):
        return document
    current = document
    for raw_token in pointer.lstrip("#").split("/"):
        if raw_token == "":
            continue
        token = raw_token.replace("~1", "/").replace("~0", "~")
        if isinstance(current, dict) and token in current:
            current = current[token]
        elif isinstance(current, list) and token.isdigit() and int(token) < len(current):
            current = current[int(token)]
        else:
            return _MISSING
    return current


def _walk_refs(value: Any) -> Any:
    if isinstance(value, dict):
        for key, item in value.items():
            if key == "$ref" and isinstance(item, str):
                yield item
            else:
                yield from _walk_refs(item)
    elif isinstance(value, list):
        for item in value:
            yield from _walk_refs(item)


def check_refs() -> list[str]:
    """Every local $ref in JSON schemas and OpenAPI YAML must resolve."""
    errors: list[str] = []

    for path in sorted(SPEC_ROOT.rglob("*.json")):
        if _is_archived(path):
            continue
        try:
            document = _load_json(path)
        except (OSError, json.JSONDecodeError) as exc:
            errors.append(f"invalid JSON {path.relative_to(SPEC_ROOT)}: {exc}")
            continue
        for ref in _walk_refs(document):
            if _is_remote(ref):
                continue
            if ref.startswith("#"):
                if _resolve_pointer(document, ref) is _MISSING:
                    errors.append(f"unresolved internal $ref {ref} in {path.relative_to(SPEC_ROOT)}")
                continue
            target = (path.parent / ref.split("#", 1)[0]).resolve()
            if not target.exists():
                errors.append(f"unresolved $ref {ref} in {path.relative_to(SPEC_ROOT)}")

    for path in sorted(SPEC_ROOT.rglob("*.yaml")) + sorted(SPEC_ROOT.rglob("*.yml")):
        if _is_archived(path):
            continue
        text = path.read_text(encoding="utf-8")
        for ref in _YAML_REF.findall(text):
            if _is_remote(ref) or ref.startswith("#"):
                continue
            target = (path.parent / ref.split("#", 1)[0]).resolve()
            if not target.exists():
                errors.append(f"unresolved $ref {ref} in {path.relative_to(SPEC_ROOT)}")

    return errors


def check_readme_example() -> list[str]:
    """The README example event must satisfy the canonical schema's constraints."""
    errors: list[str] = []
    readme = (SPEC_ROOT / "README.md").read_text(encoding="utf-8")
    marker = "### Example Event"
    if marker not in readme:
        return ["README.md is missing the '### Example Event' section"]
    section = readme.split(marker, 1)[1]
    match = re.search(r"```json\s*(\{.*?\})\s*```", section, re.DOTALL)
    if not match:
        return ["README.md example event is not a JSON code block"]
    try:
        example = json.loads(match.group(1))
    except json.JSONDecodeError as exc:
        return [f"README.md example event is invalid JSON: {exc}"]

    schema = _load_json(CANONICAL_SCHEMA_DIR / "event.schema.json")
    for field in schema.get("required", []):
        if field not in example:
            errors.append(f"README example missing required field {field!r}")

    properties = schema.get("properties", {})
    for field, value in example.items():
        constraint = properties.get(field, {})
        if "enum" in constraint and value not in constraint["enum"]:
            errors.append(f"README example field {field!r} has value outside its enum: {value!r}")
    if "event_type" in example:
        errors.append("README example uses legacy 'event_type' alias; use 'event'")
    return errors


def check_auth_docs() -> list[str]:
    """One auth mechanism: Authorization: Bearer, with a declared scheme."""
    errors: list[str] = []
    for path in OPENAPI_FILES:
        text = path.read_text(encoding="utf-8")
        if "X-LOZA-API-Key" in text and "not part of this contract" not in text and "not used by this contract" not in text:
            errors.append(f"{path.relative_to(SPEC_ROOT)} still documents X-LOZA-API-Key as a header")
        if "bearerAuth" not in text or "scheme: bearer" not in text:
            errors.append(f"{path.relative_to(SPEC_ROOT)} is missing the bearerAuth security scheme")
        if "Authorization: Bearer" not in text:
            errors.append(f"{path.relative_to(SPEC_ROOT)} does not document Authorization: Bearer")
    for path in AUTH_DOCS:
        text = path.read_text(encoding="utf-8")
        if "Authorization: Bearer" not in text:
            errors.append(f"{path.relative_to(SPEC_ROOT)} does not document Authorization: Bearer")
        if re.search(r"\blx\b", text):
            errors.append(f"{path.relative_to(SPEC_ROOT)} still uses the 'lx' prefix typo")
    return errors


def check_registry_integrity() -> list[str]:
    """Registry metadata must match the artifact and the generated contract."""
    errors: list[str] = []
    registry_dir = SPEC_ROOT / "generated" / "registry"
    manifest = _load_json(registry_dir / "manifest.json")
    artifact = (registry_dir / str(manifest["artifact"])).resolve()
    if not artifact.exists():
        return [f"registry artifact missing: {artifact}"]

    digest = hashlib.sha256(artifact.read_bytes()).hexdigest()
    if manifest.get("sha256") != digest:
        errors.append(f"registry manifest sha256 mismatch: {manifest.get('sha256')} != {digest}")
    size = artifact.stat().st_size
    if manifest.get("size") != size:
        errors.append(f"registry manifest size mismatch: {manifest.get('size')} != {size}")

    generated = (SPEC_ROOT / "generated" / "contract" / "loza-contract.json").resolve()
    if generated.exists() and artifact.read_bytes() != generated.read_bytes():
        errors.append(f"registry artifact is stale: {artifact} != {generated}")

    contract = _load_json(generated)
    for name, rel_path in contract.get("schemas", {}).items():
        if not (REPO_ROOT / rel_path).exists():
            errors.append(f"contract schema path {name!r} does not exist: {rel_path}")
    cortex_generated = SPEC_ROOT / "generated" / "contract" / "cortex-contract.json"
    cortex_contract = _load_json(cortex_generated)
    for name, rel_path in cortex_contract.get("schemas", {}).items():
        if not (REPO_ROOT / rel_path).is_file():
            errors.append(
                f"Cortex contract schema path {name!r} does not exist: {rel_path}"
            )

    return errors


def check_strict_schema_contract() -> list[str]:
    """Conformance must use the published strict schema definition."""
    errors: list[str] = []
    strict_schema = _load_json(CANONICAL_SCHEMA_DIR / "event.strict.schema.json")
    event_schema = _load_json(CANONICAL_SCHEMA_DIR / "event.schema.json")
    # Self-contained definition: re-declare the base schema with a closed root.
    # An allOf + sibling unevaluatedProperties definition is not portable
    # (jsonschema 4.26 does not propagate $ref properties annotations across
    # allOf branches, so every valid event is rejected).
    if strict_schema.get("additionalProperties") is not False:
        errors.append("published strict schema must set root additionalProperties: false")
    if strict_schema.get("properties") != event_schema.get("properties"):
        errors.append("published strict schema properties drifted from the canonical event schema")
    if strict_schema.get("required") != event_schema.get("required"):
        errors.append("published strict schema required fields drifted from the canonical event schema")
    if "$ref" in strict_schema or "allOf" in strict_schema:
        errors.append("published strict schema must be self-contained, not an allOf/$ref wrapper")

    manifest = _load_json(SPEC_ROOT / "conformance" / "manifest.json")
    expected = "../schemas/json/event.strict.schema.json"
    if manifest.get("strict_schema") != expected:
        errors.append(f"conformance manifest strict_schema must be {expected!r}")

    checker = (SPEC_ROOT / "scripts" / "check_conformance.py").read_text(encoding="utf-8")
    if 'schema_override=strict_event_schema' in checker or 'strict_event_schema["additionalProperties"]' in checker:
        errors.append("check_conformance.py still synthesizes a second strict schema")
    if "_build_validator(strict_schema" not in checker:
        errors.append("check_conformance.py must build its strict validator from the published schema")
    return errors


CHECKS = (
    ("refs", check_refs),
    ("readme example", check_readme_example),
    ("auth docs", check_auth_docs),
    ("registry integrity", check_registry_integrity),
    ("strict schema", check_strict_schema_contract),
)


def main() -> int:
    failed = False
    for label, check in CHECKS:
        errors = check()
        if errors:
            failed = True
            print(f"✗ {label}")
            for error in errors:
                print(f"    {error}")
        else:
            print(f"✓ {label}")
    if failed:
        print("\n✗ spec contract checks failed")
        return 1
    print("\n✓ spec contract checks passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
