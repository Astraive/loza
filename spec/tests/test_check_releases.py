"""Regression tests for spec/scripts/check_releases.py.

The script validates release folder *structure*; immutability enforcement
(git diff + CHANGELOG requirement) is a separate CI job. The module docstring
and function name used to claim enforcement that this code never performed.
"""

import importlib.util
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
SCRIPT_PATH = REPO_ROOT / "spec" / "scripts" / "check_releases.py"


def _load_module():
    spec = importlib.util.spec_from_file_location("check_releases", SCRIPT_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class TestCheckReleasesDocs(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.module = _load_module()

    def test_module_docstring_does_not_claim_immutability_enforcement(self):
        doc = self.module.__doc__ or ""
        self.assertNotIn(
            "Enforces that releases/ folder changes require explicit version bump",
            doc,
            "module docstring still claims enforcement this script does not do",
        )
        self.assertIn("structure", doc.lower())

    def test_structure_function_docstring_describes_only_structure(self):
        doc = self.module.check_releases_structure.__doc__ or ""
        self.assertNotIn("Verify releases/ folder integrity", doc)
        self.assertIn("structure", doc.lower())
        self.assertIn("immutability", doc.lower())

    def test_deprecated_alias_is_documented_and_delegates(self):
        alias = self.module.check_releases_immutability
        self.assertIn("deprecated", (alias.__doc__ or "").lower())
        self.assertIn("structure", (alias.__doc__ or "").lower())
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.assertEqual(
                alias(root),
                self.module.check_releases_structure(root),
            )


class TestCheckReleasesStructure(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.module = _load_module()

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)

    def tearDown(self):
        self._tmp.cleanup()

    def _releases(self):
        path = self.root / "releases"
        path.mkdir(exist_ok=True)
        return path

    def test_missing_releases_dir_is_valid(self):
        ok, messages = self.module.check_releases_structure(self.root)
        self.assertTrue(ok)
        self.assertTrue(any("does not exist" in m for m in messages))

    def test_readme_release_is_valid(self):
        release = self._releases() / "v1"
        release.mkdir()
        (release / "README.md").write_text("# v1", encoding="utf-8")

        ok, messages = self.module.check_releases_structure(self.root)
        self.assertTrue(ok, messages)
        self.assertTrue(any("README.md" in m for m in messages))

    def test_manifest_only_release_is_valid(self):
        release = self._releases() / "v0.0.1"
        release.mkdir()
        (release / "manifest.json").write_text("{}", encoding="utf-8")

        ok, messages = self.module.check_releases_structure(self.root)
        self.assertTrue(ok, messages)
        self.assertTrue(any("manifest.json" in m for m in messages))

    def test_release_without_docs_is_invalid(self):
        release = self._releases() / "v2.0.0"
        release.mkdir()

        ok, messages = self.module.check_releases_structure(self.root)
        self.assertFalse(ok)
        self.assertTrue(any("no README.md or manifest.json" in m for m in messages))

    def test_non_semantic_version_name_is_invalid(self):
        release = self._releases() / "latest"
        release.mkdir()
        (release / "README.md").write_text("# latest", encoding="utf-8")

        ok, messages = self.module.check_releases_structure(self.root)
        self.assertFalse(ok)
        self.assertTrue(any("invalid version name" in m for m in messages))

    def test_plain_file_in_releases_is_ignored(self):
        self._releases().joinpath("notes.txt").write_text("hi", encoding="utf-8")

        ok, _ = self.module.check_releases_structure(self.root)
        self.assertTrue(ok)


if __name__ == "__main__":
    unittest.main()
