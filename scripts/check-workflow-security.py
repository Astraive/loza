#!/usr/bin/env python3
"""Fail CI on unpinned GitHub Actions or broad top-level workflow permissions.

This is a small, dependency-free enforcement check for the two rules that
matter most for supply-chain safety in GitHub Actions:

1. Every external ``uses:`` reference must have the form
   ``<owner>/<repo>@<40-character-sha>`` (local ``./`` reusable workflows are
   exempt). A bare SHA is not a valid action reference and is rejected.
2. Every workflow must declare a top-level ``permissions:`` block, and that
   block must not grant any ``write`` scope. Jobs that need write access must
   opt in explicitly with a job-level ``permissions:`` block.

Run from anywhere:

    python scripts/check-workflow-security.py
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

WORKFLOWS_DIR = Path(__file__).resolve().parent.parent / ".github" / "workflows"

USES_RE = re.compile(r"^\s*(?:-\s*)?uses:\s*(\S+)")
PINNED_ACTION_RE = re.compile(
    r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*@[0-9a-f]{40}$"
)
SCOPE_RE = re.compile(r"^\s+([A-Za-z][A-Za-z0-9_-]*):\s*(\S+)")
WRITE_VALUES = {"write", "all"}


def check_file(text: str, name: str = "<workflow>") -> list[str]:
    """Return a list of human-readable findings for one workflow file."""
    findings: list[str] = []
    lines = text.splitlines()

    # Rule 1: all external actions must be pinned to a full commit SHA.
    for lineno, line in enumerate(lines, start=1):
        match = USES_RE.match(line)
        if match is None:
            continue
        ref = match.group(1)
        if ref.startswith("./"):
            continue  # local reusable workflow, nothing to pin
        if PINNED_ACTION_RE.fullmatch(ref) is None:
            findings.append(
                f"{name}:{lineno}: action must be "
                f"<owner>/<repo>@<40-character-sha>, got {ref!r}"
            )

    # Rule 2: top-level permissions must exist and be read-only.
    top_level_index = None
    for index, line in enumerate(lines):
        if line.startswith("permissions:"):
            top_level_index = index
            break

    if top_level_index is None:
        findings.append(f"{name}: missing top-level 'permissions:' block")
        return findings

    header = lines[top_level_index].split(":", 1)[1].strip().lower()
    if header in WRITE_VALUES | {"write-all"}:
        findings.append(
            f"{name}:{top_level_index + 1}: top-level permissions grant "
            f"'{header}'; use read-only defaults and per-job escalation"
        )

    for offset, line in enumerate(lines[top_level_index + 1 :], start=top_level_index + 2):
        if not line.strip():
            continue
        indent = len(line) - len(line.lstrip())
        if indent == 0:
            break  # end of the top-level permissions block
        scope_match = SCOPE_RE.match(line)
        if scope_match is None:
            continue
        scope, value = scope_match.group(1), scope_match.group(2).strip().lower()
        if value in WRITE_VALUES:
            findings.append(
                f"{name}:{offset}: top-level permission '{scope}: {value}' is "
                "too broad; move it to the job that needs it"
            )

    return findings


def check_directory(directory: Path = WORKFLOWS_DIR) -> list[str]:
    """Check every workflow file in *directory*; return all findings."""
    if not directory.is_dir():
        return [f"{directory}: workflow directory not found"]
    findings: list[str] = []
    for path in sorted(directory.glob("*.y*ml")):
        findings.extend(check_file(path.read_text(encoding="utf-8"), path.name))
    return findings


def main(argv: list[str] | None = None) -> int:
    argv = sys.argv[1:] if argv is None else argv
    directory = Path(argv[0]) if argv else WORKFLOWS_DIR
    findings = check_directory(directory)
    if findings:
        print("Workflow security check FAILED:", file=sys.stderr)
        for finding in findings:
            print(f"  - {finding}", file=sys.stderr)
        return 1
    print("Workflow security check passed: actions pinned, permissions scoped.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
