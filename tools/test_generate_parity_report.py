#!/usr/bin/env python3
"""Regression tests for tools/generate-parity-report.py."""

import importlib.util
from pathlib import Path

_SCRIPT = Path(__file__).resolve().parent / "generate-parity-report.py"
_spec = importlib.util.spec_from_file_location("generate_parity_report", _SCRIPT)
assert _spec is not None and _spec.loader is not None
gpr = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(gpr)


def _manifests_with_status(status: object) -> dict[str, dict | None]:
    """Build manifests where Go has one feature with ``status`` and others are N/A."""

    manifests: dict[str, dict | None] = {sdk: None for sdk in gpr.SDKS}
    manifests["go"] = {"features": [{"name": "feature_a", "status": status}]}
    return manifests


def test_truthy_status_spellings_are_all_counted_as_supported() -> None:
    """Every equivalent spelling of a supported status must be counted."""
    spellings = [
        "Y",
        "y",
        "yes",
        "YES",
        "Yes",
        " true ",
        "TRUE",
        "supported",
        "SuPpOrTeD",
        " complete ",
        "COMPLETE",
        "done",
        "DONE",
    ]
    for spelling in spellings:
        report = gpr.generate_report(_manifests_with_status(spelling), ["feature_a"])
        assert "| feature_a | Y | -- | -- | -- |" in report, spelling
        assert "| Go | 1 | 1 | 100% |" in report, spelling


def test_status_in_feature_key_form_is_also_normalized() -> None:
    """The ``{feature: {status: ...}}`` manifest shape must behave the same."""
    manifests: dict[str, dict | None] = {sdk: None for sdk in gpr.SDKS}
    manifests["go"] = {"feature_a": {"status": " YES "}}
    report = gpr.generate_report(manifests, ["feature_a"])
    assert "| feature_a | Y | -- | -- | -- |" in report
    assert "| Go | 1 | 1 | 100% |" in report


def test_boolean_true_is_still_supported() -> None:
    """Booleans keep their existing canonical Y/N mapping."""
    report = gpr.generate_report(_manifests_with_status(True), ["feature_a"])
    assert "| feature_a | Y | -- | -- | -- |" in report
    assert "| Go | 1 | 1 | 100% |" in report

    report = gpr.generate_report(_manifests_with_status(False), ["feature_a"])
    assert "| feature_a | N | -- | -- | -- |" in report
    assert "| Go | 0 | 1 | 0% |" in report


def test_not_applicable_and_negative_statuses_are_not_counted() -> None:
    """Normalization must not turn N/A or unsupported statuses into Y."""
    for status, cell in [("N/A", "--"), (" n/a ", "--"), ("na", "--"), ("N", "N"), ("no", "no"), ("partial", "partial")]:
        report = gpr.generate_report(_manifests_with_status(status), ["feature_a"])
        assert f"| feature_a | {cell} | -- | -- | -- |" in report, status
        assert "| Go | 0 | 1 | 0% |" in report, status
import json
import re
import subprocess
import sys
import tempfile
import unittest
import unittest.mock
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("generate-parity-report.py")
SPEC = importlib.util.spec_from_file_location("generate_parity_report", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
generate_parity_report = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(generate_parity_report)


def _write_manifest(repo_root: Path, sdk: str, content: str) -> Path:
    path = repo_root / "sdks" / sdk / "docs" / "sdk-parity-manifest.json"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")
    return path


class ManifestLoadErrorTests(unittest.TestCase):
    def test_malformed_manifest_raises_instead_of_looking_missing(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            path = _write_manifest(root, "go", '{"features": [')
            with self.assertRaisesRegex(
                generate_parity_report.ManifestLoadError, re.escape(str(path))
            ):
                generate_parity_report.load_manifest(root, "go")

    def test_missing_manifest_still_returns_none(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            self.assertIsNone(
                generate_parity_report.load_manifest(Path(temporary_directory), "go")
            )

    def test_unreadable_manifest_raises_instead_of_looking_missing(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            path = _write_manifest(root, "py", "{}")
            with unittest.mock.patch("builtins.open", side_effect=PermissionError("denied")):
                with self.assertRaisesRegex(
                    generate_parity_report.ManifestLoadError, re.escape(str(path))
                ):
                    generate_parity_report.load_manifest(root, "py")

    def test_main_surfaces_error_and_does_not_emit_report(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            path = _write_manifest(root, "go", "not json")
            result = subprocess.run(
                [sys.executable, str(MODULE_PATH), "--repo-root", str(root)],
                capture_output=True,
                text=True,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("ERROR", result.stderr)
            self.assertIn(str(path), result.stderr)
            self.assertNotIn("# SDK Parity Report", result.stdout)

    def test_valid_manifest_still_generates_report(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            _write_manifest(root, "go", json.dumps({"features": ["logging"]}))
            result = subprocess.run(
                [sys.executable, str(MODULE_PATH), "--repo-root", str(root)],
                capture_output=True,
                text=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("# SDK Parity Report", result.stdout)
            self.assertIn("logging", result.stdout)


if __name__ == "__main__":
    unittest.main()

_MODULE_PATH = Path(__file__).with_name("generate-parity-report.py")
_spec = importlib.util.spec_from_file_location("generate_parity_report", _MODULE_PATH)
generate_parity_report = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(generate_parity_report)

collect_features = generate_parity_report.collect_features


def test_collect_features_ignores_non_string_names_in_dict_entries():
    """Non-string `name` values must not break sorting of collected features."""
    manifests = {
        "go": {
            "features": [
                {"name": "auth"},
                {"name": 123},
                "logging",
                {"name": None},
                {"name": ["nested"]},
            ]
        },
        "py": {"features": ["auth", {"name": "tracing"}]},
    }

    assert collect_features(manifests) == ["auth", "logging", "tracing"]


def test_collect_features_ignores_non_string_names_in_list_manifests():
    """The list-manifest branch must apply the same type validation."""
    manifests = {
        "rs": ["metrics", {"name": "auth"}, {"name": 42}, {"name": "batching"}],
    }

    assert collect_features(manifests) == ["auth", "batching", "metrics"]


def test_collect_features_preserves_string_dict_names():
    """Valid string names in dict entries are still collected."""
    manifests = {
        "js": {"features": [{"name": "zebra"}, {"name": "alpha"}]},
    }

    assert collect_features(manifests) == ["alpha", "zebra"]