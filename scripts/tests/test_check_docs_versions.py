"""Tests for the docs version-drift / historical-banner check."""

from __future__ import annotations

import importlib.util
import sys
from pathlib import Path

import pytest

SCRIPTS_DIR = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location(
    "check_docs_versions", SCRIPTS_DIR / "check_docs_versions.py"
)
assert SPEC is not None and SPEC.loader is not None
check_docs_versions = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = check_docs_versions
SPEC.loader.exec_module(check_docs_versions)


def test_current_version_reads_latest_release_heading():
    changelog = "# Changelog\n\n## [0.4.1] - 2026-08-27\n\n## [0.4.0] - 2026-08-27\n"
    assert check_docs_versions.current_version(changelog) == "0.4.1"


def test_current_version_rejects_missing_heading():
    with pytest.raises(ValueError):
        check_docs_versions.current_version("# Changelog\n\nNothing here.\n")


def test_historical_banner_required(tmp_path: Path):
    report = tmp_path / "docs" / "old-report.md"
    report.parent.mkdir()
    report.write_text("# Old report\n\nSome stale CRITICAL finding.\n", encoding="utf-8")

    errors = check_docs_versions.check_historical_banners(
        tmp_path, ("docs/old-report.md",)
    )
    assert errors and "Status: HISTORICAL" in errors[0]

    report.write_text(
        "# Old report\n\n**Status: HISTORICAL — applies to v0.2.4 only.**\n",
        encoding="utf-8",
    )
    assert check_docs_versions.check_historical_banners(
        tmp_path, ("docs/old-report.md",)
    ) == []


def test_version_claims_flag_stale_badge_and_declaration(tmp_path: Path):
    readme = tmp_path / "README.md"
    readme.write_text(
        '<img src="https://img.shields.io/badge/version-v0.2.4-green.svg" alt="Version">\n'
        "<!-- current-release: 0.2.4 -->\n",
        encoding="utf-8",
    )
    errors = check_docs_versions.check_version_claims(tmp_path, "0.4.1", ("README.md",))
    assert len(errors) == 2
    assert all("older than current release v0.4.1" in error for error in errors)


def test_version_claims_accept_current_and_newer(tmp_path: Path):
    readme = tmp_path / "README.md"
    readme.write_text(
        "version-v0.4.1\n<!-- current-release: v0.5.0 -->\n", encoding="utf-8"
    )
    assert (
        check_docs_versions.check_version_claims(tmp_path, "0.4.1", ("README.md",))
        == []
    )


def test_main_passes_on_real_repository():
    repo_root = SCRIPTS_DIR.parent
    assert check_docs_versions.main(["--root", str(repo_root)]) == 0
