from __future__ import annotations

import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("validate-manifests.py")
SPEC = importlib.util.spec_from_file_location("validate_manifests", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
validate_manifests = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(validate_manifests)


class JavaScriptPackageIdentityTests(unittest.TestCase):
    def test_scoped_package_metadata_matches_native_manifest(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            package_root = Path(temporary_directory)
            (package_root / "package.json").write_text(
                json.dumps({"name": "@astraive/loza", "version": "0.3.0"}),
                encoding="utf-8",
            )
            manifest = {
                "kind": "sdk",
                "language": "javascript",
                "version": "0.3.0",
                "paths": {"root": package_root.as_posix()},
                "publish": {"npm": {"owner": "astraive", "package": "@astraive/loza"}},
            }

            native_errors = validate_manifests.validate_native_metadata("sdk-js", manifest, package_root / "loza-js.yaml")
            publish_errors = validate_manifests.validate_publish_metadata("sdk-js", manifest, package_root / "loza-js.yaml")

        self.assertEqual(native_errors, [])
        self.assertEqual(publish_errors, [])


class NonDictPathsTests(unittest.TestCase):
    def test_docker_publish_metadata_does_not_crash_on_non_dict_paths(self) -> None:
        manifest = {
            "kind": "docker",
            "version": "0.3.0",
            "paths": "not-a-mapping",
            "publish": {"docker": {"owner": "astraive", "images": ["repo/img"], "dockerfile": "Dockerfile"}},
        }

        errors = validate_manifests.validate_publish_metadata("docker", manifest, Path("fake.yaml"))

        self.assertEqual(errors, ["docker: dockerfile does not exist: Dockerfile"])

    def test_component_validation_reports_error_instead_of_crashing(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            manifest_path = Path(temporary_directory) / "manifest.yaml"
            manifest_path.write_text(
                "name: img\nkind: docker\nversion: 0.3.0\ndescription: d\nlicense: MIT\n"
                "repository: r\nmodule: m\npaths: not-a-mapping\n"
                "publish:\n  docker:\n    owner: astraive\n    images: [repo/img]\n    dockerfile: Dockerfile\n",
                encoding="utf-8",
            )

            errors = validate_manifests.validate_component("docker", str(manifest_path))

        self.assertIn("docker: paths must be a mapping when present", errors)


if __name__ == "__main__":
    unittest.main()
