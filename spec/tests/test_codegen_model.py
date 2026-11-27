import sys
import tempfile
import unittest
import warnings
from pathlib import Path
from unittest.mock import patch

repo_root = Path(__file__).resolve().parents[2]
codegen_dir = repo_root / "spec" / "codegen"
sys.path.insert(0, str(codegen_dir))

import model


class TestLoadProductVersion(unittest.TestCase):
    def test_reads_declared_version_without_warning(self):
        with tempfile.TemporaryDirectory() as tmp:
            spec_root = Path(tmp)
            (spec_root / "loza-spec.yaml").write_text(
                "name: loza-spec\nversion: 0.4.1\n", encoding="utf-8"
            )
            with warnings.catch_warnings(record=True) as caught:
                warnings.simplefilter("always")
                version = model._load_product_version(spec_root)

            self.assertEqual(version, "0.4.1")
            self.assertEqual(caught, [])

    def test_unreadable_specs_warn_and_return_current_fallback(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            spec_root = base / "spec"
            spec_root.mkdir()
            # Both candidate spec files exist but fail to read.
            (spec_root / "loza-spec.yaml").write_text("version: 0.4.1\n", encoding="utf-8")
            (base / "loza-spec.yaml").write_text("version: 0.4.1\n", encoding="utf-8")

            with patch.object(Path, "read_text", side_effect=OSError("permission denied")):
                with warnings.catch_warnings(record=True) as caught:
                    warnings.simplefilter("always")
                    version = model._load_product_version(spec_root)

            self.assertEqual(version, "0.4.1")
            self.assertEqual(len(caught), 1)
            warning = caught[0]
            self.assertTrue(issubclass(warning.category, RuntimeWarning))
            self.assertIn("fallback version 0.4.1", str(warning.message))
            self.assertIn("unreadable", str(warning.message))

    def test_missing_specs_warn_and_return_fallback(self):
        with tempfile.TemporaryDirectory() as tmp:
            spec_root = Path(tmp) / "does-not-exist"
            with warnings.catch_warnings(record=True) as caught:
                warnings.simplefilter("always")
                version = model._load_product_version(spec_root)

            self.assertEqual(version, "0.4.1")
            self.assertEqual(len(caught), 1)
            self.assertTrue(issubclass(caught[0].category, RuntimeWarning))

    def test_fallback_matches_declared_spec_version(self):
        spec_text = (repo_root / "spec" / "loza-spec.yaml").read_text(encoding="utf-8")
        declared = next(
            line.split(":", 1)[1].strip().strip("\"'")
            for line in spec_text.splitlines()
            if line.strip().startswith("version:")
        )
        self.assertEqual(model._FALLBACK_PRODUCT_VERSION, declared)


class TestCortexContractSchemas(unittest.TestCase):
    def test_generated_contract_references_existing_schema_files(self):
        contract = model.build_cortex_contract(repo_root / "spec")
        for name, relative_path in contract["schemas"].items():
            with self.subTest(schema=name):
                self.assertTrue((repo_root / relative_path).is_file(), relative_path)

    def test_generation_fails_for_missing_schema_file(self):
        with tempfile.TemporaryDirectory() as temporary_directory:
            spec_root = Path(temporary_directory) / "spec"
            spec_root.mkdir()

            with self.assertRaisesRegex(
                FileNotFoundError, "cortex-event.schema.json"
            ):
                model.build_cortex_contract(spec_root)


if __name__ == "__main__":
    unittest.main()
