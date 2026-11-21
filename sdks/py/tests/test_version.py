"""Regression tests for cwd-independent SDK version loading."""

from __future__ import annotations

from pathlib import Path

import loza.version as version

from loza.version import SDK_VERSION, _version_from_text


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


def test_version_is_independent_of_cwd(tmp_path, monkeypatch):
    """A stray loza-py.yaml in the launch directory must not change VERSION."""
    # Simulate an install where the packaged metadata file is absent from the
    # module's directory (pyproject package-data does not include loza-py.yaml).
    fake_module = tmp_path / "sitelib" / "src" / "loza" / "version.py"
    fake_module.parent.mkdir(parents=True)
    monkeypatch.setattr(version, "__file__", str(fake_module))

    versions = {"1.1.1-from-a", "2.2.2-from-b"}
    for name, value in zip(("a", "b"), sorted(versions)):
        directory = tmp_path / name
        directory.mkdir()
        (directory / "loza-py.yaml").write_text(f"version: {value}\n", encoding="utf-8")

    monkeypatch.chdir(tmp_path / "a")
    from_a = version._load_version()
    monkeypatch.chdir(tmp_path / "b")
    from_b = version._load_version()

    assert from_a == from_b
    assert from_a not in versions


def test_version_env_override(tmp_path, monkeypatch):
    """An explicit env var can point at a metadata file without using cwd."""
    custom = tmp_path / "custom.yaml"
    custom.write_text('version: "3.3.3-env"\n', encoding="utf-8")
    monkeypatch.setenv("LOZA_PY_VERSION_FILE", str(custom))
    assert version._load_version() == "3.3.3-env"


def test_version_env_override_takes_precedence(tmp_path, monkeypatch):
    custom = tmp_path / "custom.yaml"
    custom.write_text("version: 4.4.4-env\n", encoding="utf-8")
    cwd_file = tmp_path / "loza-py.yaml"
    cwd_file.write_text("version: 5.5.5-cwd\n", encoding="utf-8")
    monkeypatch.setenv("LOZA_PY_VERSION_FILE", str(custom))
    monkeypatch.chdir(tmp_path)
    assert version._load_version() == "4.4.4-env"


def test_sdk_version_matches_bundled_metadata():
    metadata = Path(version.__file__).resolve().parent.parent.parent / "loza-py.yaml"
    expected = None
    for line in metadata.read_text(encoding="utf-8").splitlines():
        if line.strip().startswith("version:"):
            expected = line.split(":", 1)[1].strip().strip("\"'")
            break
    assert expected is not None
    assert version.SDK_VERSION == expected


def test_inline_comment_is_not_part_of_version():
    assert _version_from_text("version: 1.2.3 # comment\n") == "1.2.3"


def test_inline_comment_without_space_is_stripped():
    assert _version_from_text("version: 1.2.3#note\n") == "1.2.3"


def test_quoted_value_with_inline_comment():
    assert _version_from_text('version: "1.2.3" # comment\n') == "1.2.3"
    assert _version_from_text("version: '1.2.3' # comment\n") == "1.2.3"


def test_nested_version_key_is_ignored():
    text = "metadata:\n  version: 9.9.9\nversion: 1.2.3\n"
    assert _version_from_text(text) == "1.2.3"


def test_trailing_text_is_rejected():
    assert _version_from_text("version: 1.2.3 and more\n") is None


def test_missing_version_returns_none():
    assert _version_from_text("name: loza-py\n") is None


def test_loaded_sdk_version_is_a_clean_token():
    assert "#" not in SDK_VERSION
    assert not any(char.isspace() for char in SDK_VERSION)
