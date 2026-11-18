"""Regression tests for manifest-declared conformance schemas."""

import importlib.util
import json
import shutil
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

SPEC_ROOT = Path(__file__).resolve().parents[1]
SCRIPT = SPEC_ROOT / "scripts" / "check_conformance.py"


def _load_module():
    module_spec = importlib.util.spec_from_file_location("check_conformance_under_test", SCRIPT)
    module = importlib.util.module_from_spec(module_spec)
    module_spec.loader.exec_module(module)
    return module


class TestDeclaredEventSchemas(unittest.TestCase):
    def test_main_builds_event_validators_from_manifest_declared_paths(self):
        """Conformance must use the schemas the manifest declares, not event.schema.json."""
        module = _load_module()
        with tempfile.TemporaryDirectory() as tmp:
            spec_root = Path(tmp) / "spec"
            scripts = spec_root / "scripts"
            conformance = spec_root / "conformance"
            schema_dir = spec_root / "schema"
            scripts.mkdir(parents=True)
            conformance.mkdir()
            schema_dir.mkdir()

            for src in (SPEC_ROOT / "schema").glob("*.json"):
                shutil.copy2(src, schema_dir / src.name)

            declared_strict = schema_dir / "manifest-declared.strict.json"
            declared_loose = schema_dir / "manifest-declared.loose.json"
            shutil.copy2(schema_dir / "event.strict.schema.json", declared_strict)
            shutil.copy2(schema_dir / "event.loose.schema.json", declared_loose)

            manifest = {
                "strict_schema": "../schema/manifest-declared.strict.json",
                "loose_schema": "../schema/manifest-declared.loose.json",
            }
            (conformance / "manifest.json").write_text(json.dumps(manifest), encoding="utf-8")

            built = []
            original_build = module._build_validator

            def recording_build(schema_path, *args, **kwargs):
                built.append(Path(schema_path).resolve())
                return original_build(schema_path, *args, **kwargs)

            with (
                patch.object(module, "__file__", str(scripts / "check_conformance.py")),
                patch.object(module, "_SCHEMA_DIR", schema_dir),
                patch.object(module, "_build_validator", recording_build),
            ):
                module.main()

            self.assertIn(declared_strict.resolve(), built)
            self.assertIn(declared_loose.resolve(), built)

    def test_declared_schemas_enforce_loose_and_strict_semantics(self):
        """The canonical declared strict schema accepts canonical events and rejects aliases."""
        module = _load_module()
        manifest = module._load_json(SPEC_ROOT / "conformance" / "manifest.json")
        loose, strict = module._build_event_validators(
            SPEC_ROOT / "conformance", manifest, module._build_schema_store()
        )

        canonical = module._load_json(SPEC_ROOT / "fixtures" / "valid" / "http_success.json")
        alias = module._load_json(
            SPEC_ROOT / "fixtures" / "valid" / "loose_event_type_alias.json"
        )

        self.assertEqual(list(loose.iter_errors(canonical)), [])
        self.assertEqual(list(strict.iter_errors(canonical)), [])
        self.assertEqual(list(loose.iter_errors(alias)), [])
        self.assertTrue(list(strict.iter_errors(alias)))


if __name__ == "__main__":
    unittest.main()
