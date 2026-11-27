#!/usr/bin/env python3
"""Unit tests for the workflow security enforcement check."""

from __future__ import annotations

import importlib.util
import unittest
from pathlib import Path

_SCRIPTS_DIR = Path(__file__).resolve().parent
_SPEC = importlib.util.spec_from_file_location(
    "check_workflow_security", _SCRIPTS_DIR / "check-workflow-security.py"
)
assert _SPEC is not None and _SPEC.loader is not None
_MODULE = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(_MODULE)
check_directory = _MODULE.check_directory
check_file = _MODULE.check_file
USES_RE = _MODULE.USES_RE
PINNED_ACTION_RE = _MODULE.PINNED_ACTION_RE

SHA = "a" * 40


class CheckWorkflowSecurityTests(unittest.TestCase):
    def test_unpinned_action_is_rejected(self) -> None:
        workflow = (
            "permissions:\n"
            "  contents: read\n"
            "jobs:\n"
            "  build:\n"
            "    runs-on: ubuntu-latest\n"
            "    steps:\n"
            "      - uses: actions/checkout@v4\n"
        )
        findings = check_file(workflow, "test.yml")
        self.assertTrue(any("40-character-sha" in f for f in findings), findings)

    def test_moving_ref_is_rejected(self) -> None:
        workflow = (
            "permissions:\n"
            "  contents: read\n"
            "jobs:\n"
            "  build:\n"
            "    steps:\n"
            "      - uses: dtolnay/rust-toolchain@stable\n"
        )
        self.assertTrue(any("40-character-sha" in f for f in check_file(workflow)))

    def test_bare_sha_is_rejected(self) -> None:
        # A bare SHA is not a resolvable GitHub Actions reference: the
        # owner/repo prefix must be present too.
        workflow = (
            "permissions:\n"
            "  contents: read\n"
            "jobs:\n"
            "  build:\n"
            "    steps:\n"
            f"      - uses: {SHA} # v4\n"
        )
        findings = check_file(workflow, "bare.yml")
        self.assertTrue(any("40-character-sha" in f for f in findings), findings)
        self.assertTrue(any(SHA in f for f in findings), findings)

    def test_pinned_action_with_subpath_is_accepted(self) -> None:
        workflow = (
            "permissions:\n"
            "  contents: read\n"
            "jobs:\n"
            "  build:\n"
            "    steps:\n"
            f"      - uses: github/codeql-action/upload-sarif@{SHA} # v4\n"
        )
        self.assertEqual(check_file(workflow), [])

    def test_pinned_action_is_accepted(self) -> None:
        workflow = (
            "permissions:\n"
            "  contents: read\n"
            "jobs:\n"
            "  build:\n"
            "    steps:\n"
            f"      - uses: actions/checkout@{SHA} # v4\n"
        )
        self.assertEqual(check_file(workflow), [])

    def test_local_reusable_workflow_is_exempt(self) -> None:
        workflow = (
            "permissions:\n"
            "  contents: read\n"
            "jobs:\n"
            "  call:\n"
            "    uses: ./.github/workflows/publish.yml\n"
        )
        self.assertEqual(check_file(workflow), [])

    def test_top_level_write_is_rejected(self) -> None:
        workflow = (
            "permissions:\n"
            "  contents: write\n"
            "  id-token: write\n"
            "jobs:\n"
            "  build:\n"
            "    steps:\n"
        )
        findings = check_file(workflow, "release.yml")
        self.assertTrue(any("contents: write" in f for f in findings), findings)
        self.assertTrue(any("id-token: write" in f for f in findings), findings)

    def test_top_level_write_all_is_rejected(self) -> None:
        findings = check_file("permissions: write-all\njobs:\n")
        self.assertTrue(any("write-all" in f for f in findings), findings)

    def test_job_level_write_is_allowed(self) -> None:
        workflow = (
            "permissions:\n"
            "  contents: read\n"
            "jobs:\n"
            "  publish:\n"
            "    permissions:\n"
            "      contents: write\n"
            "      id-token: write\n"
            "    steps:\n"
        )
        self.assertEqual(check_file(workflow), [])

    def test_missing_top_level_permissions_is_rejected(self) -> None:
        findings = check_file("jobs:\n  build:\n    steps:\n")
        self.assertTrue(any("missing top-level" in f for f in findings), findings)

    def test_real_workflows_pass(self) -> None:
        directory = Path(__file__).resolve().parent.parent / ".github" / "workflows"
        findings = check_directory(directory)
        self.assertEqual(findings, [], "\n".join(findings))

        # Explicitly assert every external ref keeps its owner/repo prefix so
        # a bare-SHA regression cannot slip through again.
        external = []
        for path in sorted(directory.glob("*.y*ml")):
            for lineno, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
                match = USES_RE.match(line)
                if match is None:
                    continue
                ref = match.group(1)
                if ref.startswith("./"):
                    continue
                external.append((path.name, lineno, ref))
                self.assertIsNotNone(
                    PINNED_ACTION_RE.fullmatch(ref),
                    f"{path.name}:{lineno}: invalid action reference {ref!r}",
                )
                self.assertNotRegex(ref, r"^[0-9a-f]{40}$", "bare SHA is not resolvable")
        self.assertEqual(len(external), 106)


if __name__ == "__main__":
    unittest.main()
