"""Load SDK version from loza-py.yaml metadata file.

Falls back to a hardcoded default if the file cannot be found or parsed.
"""

from __future__ import annotations

import os
from pathlib import Path

_FALLBACK_VERSION = "0.4.1"
_VERSION_FILE_ENV = "LOZA_PY_VERSION_FILE"


def _load_version() -> str:
    """Read version from loza-py.yaml, searching standard locations."""
    candidates: list[Path] = []

    # Explicit, caller-provided override. Never derive the SDK version from
    # the process working directory: an unrelated loza-py.yaml in cwd must not
    # be able to change VERSION reported by the SDK.
    override = os.getenv(_VERSION_FILE_ENV, "").strip()
    if override:
        candidates.append(Path(override))

    # Packaged metadata, resolved relative to this file (src/loza/version.py
    # -> sdks/py/loza-py.yaml). This is independent of where Python was
    # launched from.
    candidates.append(Path(__file__).resolve().parent.parent.parent / "loza-py.yaml")

    for path in candidates:
        if path.is_file():
            try:
                text = path.read_text(encoding="utf-8")
                for line in text.splitlines():
                    stripped = line.strip()
                    if stripped.startswith("version:"):
                        value = stripped.split(":", 1)[1].strip().strip("\"'")
                        if value:
                            return value
            except (OSError, UnicodeDecodeError):
                continue
    return _FALLBACK_VERSION


SDK_VERSION: str = _load_version()
