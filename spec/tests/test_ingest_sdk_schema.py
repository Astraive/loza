"""Validate the JavaScript SDK's real ingest envelope against active schemas."""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
from pathlib import Path

import pytest

SPEC_ROOT = Path(__file__).resolve().parents[1]
REPO_ROOT = SPEC_ROOT.parent
sys.path.insert(0, str(SPEC_ROOT / "scripts"))



def test_javascript_ingest_envelope_matches_active_schemas() -> None:
    pytest.importorskip("jsonschema")
    import check_conformance
    bun = shutil.which("bun")
    if bun is None:
        pytest.skip("Bun is required to exercise the JavaScript envelope builder")

    builder_uri = (
        REPO_ROOT / "sdks" / "js" / "src" / "generated" / "spec-contract.ts"
    ).as_uri()
    event_path = SPEC_ROOT / "fixtures" / "valid" / "minimal_event.json"
    script = f"""
import fs from "node:fs";
import {{ buildIngestEnvelope }} from {json.dumps(builder_uri)};
const event = JSON.parse(fs.readFileSync({json.dumps(str(event_path))}, "utf8"));
process.stdout.write(JSON.stringify(buildIngestEnvelope("loza-js", "0.4.1", "checkout", [event])));
"""
    result = subprocess.run(
        [bun, "--eval", script],
        cwd=REPO_ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    envelope = json.loads(result.stdout)

    schema_store = check_conformance._build_schema_store()
    for schema_path in (
        SPEC_ROOT / "schema" / "ingest.schema.json",
        SPEC_ROOT / "schemas" / "json" / "ingest.schema.json",
    ):
        validator = check_conformance._build_validator(schema_path, store=schema_store)
        errors = list(validator.iter_errors(envelope))
        assert not errors, (
            f"{schema_path.relative_to(SPEC_ROOT)} rejected the SDK envelope: "
            f"{errors[0].message}"
        )
