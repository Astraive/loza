from __future__ import annotations

import importlib.util
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("validate-inputs.py")
SPEC = importlib.util.spec_from_file_location("validate_inputs", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
validate_inputs = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(validate_inputs)


class ValidateInputsTests(unittest.TestCase):
    def test_known_components_are_loaded_from_registry(self) -> None:
        components = validate_inputs.known_components()
        self.assertTrue({"loza", "spec", "collector", "cortex", "cli", "sdk-go", "sdk-js", "sdk-py", "sdk-rs"} <= components)

    def test_accepts_valid_versions(self) -> None:
        for value in ("0.0.0", "1.2.3", "10.20.30"):
            with self.subTest(value=value):
                self.assertEqual(validate_inputs.validate_version(value), value)

    def test_rejects_malformed_versions(self) -> None:
        malformed = (
            "v1.2.3",
            "1.2",
            "1.2.3.4",
            "1.2.3-rc1",
            '1.2.3"; echo pwned',
            "x' or '1'='1",
            "$(touch /tmp/pwned)",
            "",
        )
        for value in malformed:
            with self.subTest(value=value), self.assertRaises(ValueError):
                validate_inputs.validate_version(value)

    def test_accepts_known_components(self) -> None:
        for value in ("loza", "spec", "collector", "sdk-js"):
            with self.subTest(value=value):
                self.assertEqual(validate_inputs.validate_component(value), value)

    def test_rejects_unknown_or_injected_components(self) -> None:
        components = validate_inputs.known_components()
        for value in ("UNKNOWN", "collector'; echo pwned #", "$(id)", ""):
            with self.subTest(value=value), self.assertRaises(ValueError):
                validate_inputs.validate_component(value, components)

    def test_components_list_is_validated(self) -> None:
        self.assertEqual(validate_inputs.validate_components("collector, cortex"), ["collector", "cortex"])
        for value in ("", "collector,,cortex", "collector,unknown", ",'"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                validate_inputs.validate_components(value)

    def test_cli_fails_closed_on_injected_version(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            marker = Path(temporary_directory) / "pwned"
            payload = f'1.2.3"; touch {marker} #'
            env = {"RELEASE_VERSION": payload, "PATH": os.environ.get("PATH", "")}
            result = subprocess.run(
                [sys.executable, str(MODULE_PATH)],
                env=env,
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(marker.exists(), "injected input was executed")

    def test_cli_accepts_valid_inputs(self) -> None:
        env = {
            "RELEASE_VERSION": "1.2.3",
            "RELEASE_COMPONENTS": "collector,cortex",
            "PATH": os.environ.get("PATH", ""),
        }
        result = subprocess.run(
            [sys.executable, str(MODULE_PATH)],
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
