#!/usr/bin/env python3
"""Regression tests for the OpenAPI generation workflow entrypoint."""

from __future__ import annotations

import os
import subprocess
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
ENTRYPOINT = REPO_ROOT / "tools" / "generate_openapi.sh"


class GenerateOpenAPITests(unittest.TestCase):
    def test_entrypoint_generates_expected_artifact_directories(self) -> None:
        self.assertTrue(ENTRYPOINT.is_file(), "workflow generator entrypoint is missing")
        self.assertTrue(os.access(ENTRYPOINT, os.X_OK), "generator entrypoint is not executable")

        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            docker_bin = temporary / "bin"
            docker_bin.mkdir()
            docker_stub = docker_bin / "docker"
            docker_stub.write_text(
                "#!/usr/bin/env bash\n"
                "set -euo pipefail\n"
                "while (($#)); do\n"
                "  if [[ $1 == -o ]]; then\n"
                "    relative_output=${2#/output/}\n"
                "    mkdir -p \"$OPENAPI_OUTPUT_DIR/$relative_output\"\n"
                "    printf 'generated\\n' > \"$OPENAPI_OUTPUT_DIR/$relative_output/openapi.yaml\"\n"
                "    exit 0\n"
                "  fi\n"
                "  shift\n"
                "done\n"
                "echo 'missing generator output argument' >&2\n"
                "exit 2\n",
                encoding="utf-8",
            )
            docker_stub.chmod(0o755)
            output = temporary / "openapi-output"
            environment = os.environ.copy()
            environment["PATH"] = f"{docker_bin}{os.pathsep}{environment['PATH']}"
            environment["OPENAPI_OUTPUT_DIR"] = str(output)

            subprocess.run([str(ENTRYPOINT)], cwd=REPO_ROOT, env=environment, check=True)

            for language in ("go", "python", "rust", "typescript-fetch"):
                artifact = output / language / "openapi.yaml"
                self.assertTrue(artifact.is_file(), f"missing generated artifact: {artifact}")


if __name__ == "__main__":
    unittest.main()
