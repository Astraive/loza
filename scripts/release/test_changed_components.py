from __future__ import annotations

import importlib.util
import os
import tempfile
import unittest
from pathlib import Path
from unittest import mock


MODULE_PATH = Path(__file__).with_name("changed-components.py")
SPEC = importlib.util.spec_from_file_location("changed_components", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
changed_components = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(changed_components)


def key(version: str) -> tuple[object, ...]:
    return changed_components.semver_precedence_key(changed_components.parse_semver(version, "test"))


class SemverPrecedenceTests(unittest.TestCase):
    def test_prerelease_promotion_is_an_increase(self) -> None:
        # Regression for MASS-0375: 1.2.3-rc.1 -> 1.2.3 must not be a regression.
        self.assertLess(key("1.2.3-rc.1"), key("1.2.3"))

    def test_numeric_prerelease_bump_is_an_increase(self) -> None:
        # Regression for MASS-0375: 1.2.3-alpha.1 -> 1.2.3-alpha.2 must not be a regression.
        self.assertLess(key("1.2.3-alpha.1"), key("1.2.3-alpha.2"))

    def test_build_metadata_is_ignored(self) -> None:
        self.assertEqual(key("1.2.3+build.1"), key("1.2.3"))

    def test_full_semver_precedence_ordering(self) -> None:
        ordered = [
            "1.0.0-alpha",
            "1.0.0-alpha.1",
            "1.0.0-alpha.beta",
            "1.0.0-beta",
            "1.0.0-beta.2",
            "1.0.0-beta.11",
            "1.0.0-rc.1",
            "1.0.0",
            "1.0.1",
        ]
        for lower, higher in zip(ordered, ordered[1:]):
            with self.subTest(lower=lower, higher=higher):
                self.assertLess(key(lower), key(higher))


class DetectRegressionTests(unittest.TestCase):
    def _detect(self, old_version: str, new_version: str) -> dict[str, object]:
        manifest = {"version": new_version, "kind": "sdk", "language": "python"}
        base = {"version": old_version}
        with (
            mock.patch.object(changed_components, "load_registry", return_value={"test": "test.yaml"}),
            mock.patch.object(changed_components, "load_current_yaml", return_value=manifest),
            mock.patch.object(changed_components, "load_base_yaml", return_value=base),
        ):
            return changed_components.detect("base")

    def test_prerelease_increase_is_detected_without_error(self) -> None:
        for old_version, new_version in (("1.2.3-rc.1", "1.2.3"), ("1.2.3-alpha.1", "1.2.3-alpha.2")):
            with self.subTest(old=old_version, new=new_version):
                result = self._detect(old_version, new_version)
                self.assertTrue(result["test"]["changed"])
                self.assertEqual(result["test"]["old_version"], old_version)
                self.assertEqual(result["test"]["new_version"], new_version)

    def test_true_regression_is_still_rejected(self) -> None:
        with self.assertRaises(SystemExit):
            self._detect("1.3.0", "1.2.4")


class WriteGithubOutputTests(unittest.TestCase):
    def test_unset_github_output_is_a_noop(self) -> None:
        values = {"has_changes": "true"}

        with mock.patch.dict(os.environ, {}, clear=True):
            self.assertNotIn("GITHUB_OUTPUT", os.environ)
            # The buggy version turned "" into Path(".") and raised IsADirectoryError.
            changed_components.write_github_output(values)

    def test_empty_github_output_is_a_noop(self) -> None:
        with mock.patch.dict(os.environ, {"GITHUB_OUTPUT": ""}, clear=True):
            self.assertEqual(os.environ.get("GITHUB_OUTPUT"), "")
            changed_components.write_github_output({"has_changes": "true"})

    def test_writes_values_when_github_output_is_set(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            output_path = Path(temporary_directory) / "github_output"
            values = {"has_changes": "true", "changed_components": "spec,cli"}

            with mock.patch.dict(os.environ, {"GITHUB_OUTPUT": str(output_path)}, clear=True):
                changed_components.write_github_output(values)

            self.assertEqual(
                output_path.read_text(encoding="utf-8"),
                "has_changes=true\nchanged_components=spec,cli\n",
            )


if __name__ == "__main__":
    unittest.main()
