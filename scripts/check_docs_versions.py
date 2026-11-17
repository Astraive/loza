#!/usr/bin/env python3
"""Check documentation for version drift and required historical banners.

The check has two parts:

1. Every report listed in ``HISTORICAL_DOCS`` must carry a
   ``Status: HISTORICAL`` banner so readers cannot mistake stale CRITICAL/BLOCK
   findings for current risk.
2. Docs that make a claim about the current release (``CURRENT_FACING_DOCS``)
   must not advertise a version older than the top entry in ``CHANGELOG.md``.

Historical reports, migration guides and release notes intentionally mention
old versions, so they are deliberately excluded from the claim scan.

Exit status is 0 when all checks pass, 1 otherwise. No third-party deps.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent

HISTORICAL_MARKER = "Status: HISTORICAL"

HISTORICAL_DOCS = (
    "docs/api-security-v0.2.4.md",
    "docs/code-review-final-v0.2.4.md",
    "docs/regression-report.md",
    "docs/benchmark-report.md",
    "docs/audit-v0.2.4-variable-manager-report.md",
)

CURRENT_FACING_DOCS = (
    "README.md",
    "docs/security.md",
)

CHANGELOG_VERSION_RE = re.compile(r"^##\s*\[(?P<version>\d+\.\d+\.\d+)\]", re.MULTILINE)

# A shields.io version badge (version-vX.Y.Z) or an explicit, machine-readable
# declaration (<!-- current-release: X.Y.Z -->) is treated as a current-version claim.
VERSION_CLAIM_PATTERNS = (
    re.compile(r"version-v(?P<version>\d+\.\d+\.\d+)"),
    re.compile(
        r"<!--\s*current-release:\s*v?(?P<version>\d+\.\d+\.\d+)\s*-->",
        re.IGNORECASE,
    ),
)


def parse_version(version: str) -> tuple[int, int, int]:
    """Return a comparable tuple for a dotted numeric version string."""
    return tuple(int(part) for part in version.split("."))  # type: ignore[return-value]


def current_version(changelog_text: str) -> str:
    """Extract the newest release version from CHANGELOG.md."""
    match = CHANGELOG_VERSION_RE.search(changelog_text)
    if match is None:
        raise ValueError("could not find a '## [X.Y.Z]' release heading in CHANGELOG.md")
    return match.group("version")


def check_historical_banners(root: Path, docs: tuple[str, ...] = HISTORICAL_DOCS) -> list[str]:
    """Return one error per historical report missing its banner."""
    errors: list[str] = []
    for rel in docs:
        path = root / rel
        if not path.is_file():
            errors.append(f"{rel}: historical report is missing")
            continue
        if HISTORICAL_MARKER not in path.read_text(encoding="utf-8"):
            errors.append(f"{rel}: missing required '{HISTORICAL_MARKER}' banner")
    return errors


def check_version_claims(
    root: Path,
    version: str,
    docs: tuple[str, ...] = CURRENT_FACING_DOCS,
) -> list[str]:
    """Return one error per stale current-version claim in ``docs``."""
    errors: list[str] = []
    current = parse_version(version)
    for rel in docs:
        path = root / rel
        if not path.is_file():
            errors.append(f"{rel}: current-facing doc is missing")
            continue
        for lineno, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
            for pattern in VERSION_CLAIM_PATTERNS:
                for match in pattern.finditer(line):
                    found = match.group("version")
                    if parse_version(found) < current:
                        errors.append(
                            f"{rel}:{lineno}: version claim v{found} is older than "
                            f"current release v{version}"
                        )
    return errors


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--root",
        type=Path,
        default=REPO_ROOT,
        help="repository root (default: parent of this script's directory)",
    )
    args = parser.parse_args(argv)

    changelog = args.root / "CHANGELOG.md"
    if not changelog.is_file():
        print(f"FAIL: {changelog} not found")
        return 1
    try:
        version = current_version(changelog.read_text(encoding="utf-8"))
    except ValueError as exc:
        print(f"FAIL: {exc}")
        return 1

    errors = check_historical_banners(args.root)
    errors += check_version_claims(args.root, version)
    if errors:
        print("FAIL: docs version-drift check")
        for error in errors:
            print(f"  - {error}")
        return 1

    print(
        f"OK: historical banners present and current-version claims match v{version}"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
