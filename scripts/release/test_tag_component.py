from __future__ import annotations

import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path

import yaml


MODULE_PATH = Path(__file__).with_name("tag-component.py")
SPEC = importlib.util.spec_from_file_location("tag_component", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
tag_component = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(tag_component)


class ValidateVersionTests(unittest.TestCase):
    def test_accepts_semantic_versions(self) -> None:
        for version in (
            "1.2.3",
            "0.0.0",
            "10.20.30",
            "1.2.3-alpha.1",
            "1.2.3+build.5",
            "1.2.3-alpha.1+build.001",
        ):
            with self.subTest(version=version):
                tag_component.validate_version(version)

    def test_rejects_malformed_versions(self) -> None:
        for version in (
            "1.2",
            "abc",
            "1.2.3.4",
            "01.2.3",
            "1.2.3 ",
            "1.2.3/4",
            "1.2.3-01",
            "1.2.3-alpha..1",
            "1.2.3+build..x",
            "1.2.3\n",
        ):
            with self.subTest(version=version):
                with self.assertRaises(ValueError):
                    tag_component.validate_version(version)

    def test_rejects_leading_v(self) -> None:
        with self.assertRaises(ValueError):
            tag_component.validate_version("v1.2.3")

    def test_rejects_empty_version(self) -> None:
        with self.assertRaises(ValueError):
            tag_component.validate_version("")


class TagGenerationTests(unittest.TestCase):
    def test_dry_run_does_not_create_tag_for_invalid_version(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            manifest_path = root / "loza.yaml"
            manifest_path.write_text(
                yaml.safe_dump({"version": "1.2"}),
                encoding="utf-8",
            )
            (root / "release.yaml").write_text(
                yaml.safe_dump({"components": {"loza": "loza.yaml"}}),
                encoding="utf-8",
            )

            original_root = tag_component.ROOT
            tag_component.ROOT = root
            try:
                original_argv = sys.argv
                sys.argv = ["tag-component.py", "loza", "--dry-run"]
                try:
                    result = tag_component.main()
                finally:
                    sys.argv = original_argv
            finally:
                tag_component.ROOT = original_root

        self.assertEqual(result, 1)

    def test_dry_run_rejects_whitespace_in_manifest_version(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            manifest_path = root / "loza.yaml"
            manifest_path.write_text(
                yaml.safe_dump({"version": "1.2.3\n"}),
                encoding="utf-8",
            )
            (root / "release.yaml").write_text(
                yaml.safe_dump({"components": {"loza": "loza.yaml"}}),
                encoding="utf-8",
            )

            original_root = tag_component.ROOT
            tag_component.ROOT = root
            try:
                original_argv = sys.argv
                sys.argv = ["tag-component.py", "loza", "--dry-run"]
                try:
                    result = tag_component.main()
                finally:
                    sys.argv = original_argv
            finally:
                tag_component.ROOT = original_root

        self.assertEqual(result, 1)


if __name__ == "__main__":
    unittest.main()
