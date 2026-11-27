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

CHANGED_COMPONENTS_PATH = Path(__file__).with_name("changed-components.py")
CHANGED_SPEC = importlib.util.spec_from_file_location("changed_components", CHANGED_COMPONENTS_PATH)
assert CHANGED_SPEC is not None and CHANGED_SPEC.loader is not None
changed_components = importlib.util.module_from_spec(CHANGED_SPEC)
CHANGED_SPEC.loader.exec_module(changed_components)


def write_release_manifest(directory: Path, version: str) -> Path:
    manifest_path = directory / "release.yaml"
    manifest_path.write_text(
        "\n".join(
            [
                "name: loza",
                "kind: release",
                f"version: {version}",
                "description: test manifest",
                "license: Apache-2.0",
                "repository: https://github.com/Astraive/loza",
                "module: github.com/Astraive/loza",
                "publish:",
                "  github_release:",
                "    tag_prefix: v",
                "    owner: astraive",
            ]
        ),
        encoding="utf-8",
    )
    return manifest_path


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


class SemverValidationTests(unittest.TestCase):
    def test_prerelease_and_build_version_validates(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            manifest_path = write_release_manifest(Path(temporary_directory), "1.2.3-rc.1+build.5")
            errors = validate_manifests.validate_component("loza", manifest_path.as_posix())

        self.assertEqual(errors, [])

    def test_semver_regex_accepts_prerelease_and_build(self) -> None:
        pattern = validate_manifests.SEMVER_RE
        self.assertTrue(pattern.match("1.2.3"))
        self.assertTrue(pattern.match("1.2.3-rc.1"))
        self.assertTrue(pattern.match("1.2.3+build.5"))
        self.assertTrue(pattern.match("1.2.3-rc.1+build.5"))
        self.assertFalse(pattern.match("v1.2.3"))
        self.assertFalse(pattern.match("01.2.3"))

    def test_changed_components_parses_prerelease_and_build(self) -> None:
        version = "1.2.3-rc.1+build.5"
        self.assertEqual(changed_components.parse_semver(version, "loza"), (1, 2, 3, version))


class MalformedNativeMetadataTests(unittest.TestCase):
    def assert_parse_error_collected(
        self, component: str, filename: str, malformed_content: str, manifest_name: str
    ) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            package_root = Path(temporary_directory)
            (package_root / filename).write_text(malformed_content, encoding="utf-8")
            manifest = {
                "kind": "sdk",
                "version": "0.3.0",
                "paths": {"root": package_root.as_posix()},
            }

            errors = validate_manifests.validate_native_metadata(
                component, manifest, package_root / manifest_name
            )

        self.assertEqual(len(errors), 1)
        self.assertIn("failed to parse", errors[0])
        self.assertIn(filename, errors[0])

    def test_malformed_package_json_is_collected(self) -> None:
        self.assert_parse_error_collected(
            "sdk-js", "package.json", "{ this is not valid json", "loza-js.yaml"
        )

    def test_malformed_pyproject_toml_is_collected(self) -> None:
        self.assert_parse_error_collected(
            "sdk-py", "pyproject.toml", "[project\nname = 'loza'", "loza-py.yaml"
        )

    def test_malformed_cargo_toml_is_collected(self) -> None:
        self.assert_parse_error_collected(
            "sdk-rs", "Cargo.toml", "[package\nname = 'loza'", "loza-rs.yaml"
        )

    def test_non_mapping_native_metadata_is_collected(self) -> None:
        cases = (
            ("sdk-js", "package.json", "[]", "loza-js.yaml", "must contain a JSON object"),
            ("sdk-py", "pyproject.toml", 'project = "unexpected"\n', "loza-py.yaml", "project must be a mapping"),
            ("sdk-rs", "Cargo.toml", 'package = "unexpected"\n', "loza-rs.yaml", "package must be a mapping"),
        )
        for component, filename, content, manifest_name, expected_error in cases:
            with self.subTest(component=component):
                with tempfile.TemporaryDirectory() as temporary_directory:
                    package_root = Path(temporary_directory)
                    (package_root / filename).write_text(content, encoding="utf-8")
                    manifest = {
                        "version": "0.3.0",
                        "paths": {"root": package_root.as_posix()},
                    }
                    errors = validate_manifests.validate_native_metadata(
                        component, manifest, package_root / manifest_name
                    )

                self.assertEqual(len(errors), 1, errors)
                self.assertIn(filename, errors[0])
                self.assertIn(expected_error, errors[0])


if __name__ == "__main__":
    unittest.main()
