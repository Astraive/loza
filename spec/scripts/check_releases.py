#!/usr/bin/env python3
"""
Validate the structure of the frozen release snapshots in releases/.

This check verifies that each release directory uses a semantic version name
and contains a README.md or manifest.json documenting its contents. It does
*not* enforce release immutability: rejecting changes to releases/ in a pull
request (and requiring a CHANGELOG update) is handled by the
``release-protection`` job in .github/workflows/spec-ci.yml.
"""

from __future__ import annotations

import sys
from pathlib import Path

if sys.stdout.encoding and sys.stdout.encoding.lower() != "utf-8":
    sys.stdout.reconfigure(encoding="utf-8")  # type: ignore[union-attr]
    sys.stderr.reconfigure(encoding="utf-8")  # type: ignore[union-attr]


def check_releases_structure(spec_root: Path | None = None) -> tuple[bool, list[str]]:
    """
    Verify that the releases/ folder has a valid structure.

    Returns (is_valid, messages).
    Checks:
    - Each release has a README.md or manifest.json documenting its contents
    - Releases are named semantically (v1, v0.0.1, v2.0.0, etc.)

    This is a structure check only. Immutability enforcement (rejecting
    changes to releases/ in a PR and requiring a CHANGELOG update) lives in
    the ``release-protection`` job of .github/workflows/spec-ci.yml.

    ``spec_root`` may be overridden to check an alternate tree; it defaults
    to the spec/ directory containing this script.
    """
    if spec_root is None:
        spec_root = Path(__file__).resolve().parents[1]
    releases_dir = spec_root / "releases"

    messages = []
    issues = []

    if not releases_dir.exists():
        messages.append("⊘ releases/ folder does not exist (not yet created)")
        return True, messages

    # Check each version folder
    for version_dir in sorted(releases_dir.iterdir()):
        if not version_dir.is_dir():
            continue

        version_name = version_dir.name

        # Verify version name is semantic
        if not _is_semantic_version(version_name):
            issues.append(f"✗ invalid version name: {version_name} (expected v1, v2.0.0, etc.)")
            continue

        # Check for README or manifest
        readme = version_dir / "README.md"
        manifest = version_dir / "manifest.json"

        if readme.exists():
            messages.append(f"✓ {version_dir.relative_to(spec_root)}/README.md")
        elif manifest.exists():
            messages.append(f"✓ {version_dir.relative_to(spec_root)}/manifest.json")
        else:
            issues.append(f"⊘ {version_name}/ has no README.md or manifest.json")

        # List contents
        contents = [f for f in version_dir.iterdir() if f.name not in {".git", ".gitignore"}]
        if contents:
            messages.append(f"  └─ contains {len(contents)} file(s)/folder(s)")

    if issues:
        for issue in issues:
            messages.append(issue)
        return False, messages

    messages.append(f"✓ releases/ folder structure is valid")
    return True, messages


def check_releases_immutability(spec_root: Path | None = None) -> tuple[bool, list[str]]:
    """Deprecated alias for :func:`check_releases_structure`.

    The previous name overstated what this check does: it validates release
    folder structure, not immutability. Immutability enforcement is performed
    by the ``release-protection`` job in .github/workflows/spec-ci.yml.
    Kept as an alias so existing callers keep working; use
    ``check_releases_structure`` in new code.
    """
    return check_releases_structure(spec_root)


def _is_semantic_version(name: str) -> bool:
    """Check if name looks like a version (v1, v1.0, v0.0.1, etc.)."""
    if not name.startswith("v"):
        return False
    version_part = name[1:]
    parts = version_part.split(".")
    return all(p.isdigit() for p in parts) and len(parts) >= 1


def main() -> int:
    is_valid, messages = check_releases_structure()

    for msg in messages:
        print(msg)

    return 0 if is_valid else 1


if __name__ == "__main__":
    raise SystemExit(main())
