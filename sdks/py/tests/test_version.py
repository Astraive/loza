from __future__ import annotations

from pathlib import Path

import loza.version as version


def _point_candidates_at(tmp_path: Path, monkeypatch) -> Path:
    """Redirect both metadata search locations to a temporary directory."""
    metadata = tmp_path / "loza-py.yaml"
    monkeypatch.setattr(version, "__file__", str(tmp_path / "src" / "loza" / "version.py"))
    monkeypatch.chdir(tmp_path)
    return metadata


def test_load_version_falls_back_on_non_utf8_metadata(tmp_path, monkeypatch) -> None:
    metadata = _point_candidates_at(tmp_path, monkeypatch)
    metadata.write_bytes(b"version: 9.9.9\nname: \xff\xfe\n")

    assert version._load_version() == version._FALLBACK_VERSION


def test_load_version_parses_valid_metadata(tmp_path, monkeypatch) -> None:
    metadata = _point_candidates_at(tmp_path, monkeypatch)
    metadata.write_text("name: loza-py\nversion: 1.2.3\n", encoding="utf-8")

    assert version._load_version() == "1.2.3"


def test_load_version_falls_back_when_metadata_missing(tmp_path, monkeypatch) -> None:
    _point_candidates_at(tmp_path, monkeypatch)

    assert version._load_version() == version._FALLBACK_VERSION
