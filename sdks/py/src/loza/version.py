"""Load SDK version from loza-py.yaml metadata file.

Falls back to a hardcoded default if the file cannot be found or parsed.
"""

from __future__ import annotations

import os
from pathlib import Path
import re

_FALLBACK_VERSION = "0.4.1"
_VERSION_FILE_ENV = "LOZA_PY_VERSION_FILE"

# Only a top-level ``version:`` key is considered; indented (nested) keys are
# ignored. The value must be a single version token, e.g. ``1.2.3`` or
# ``1.2.3-beta.1+build``.
_VERSION_LINE = re.compile(r"^version[ \t]*:[ \t]*(.*)$")
_VERSION_TOKEN = re.compile(r"[0-9A-Za-z][0-9A-Za-z.+-]*")


def _strip_inline_comment(value: str) -> str:
    """Drop a trailing ``#`` comment that is not inside a quoted string."""
    quote = ""
    for index, char in enumerate(value):
        if quote:
            if char == quote:
                quote = ""
        elif char in ("'", '"'):
            quote = char
        elif char == "#":
            return value[:index]
    return value


def _version_from_text(text: str) -> str | None:
    """Return the first valid top-level version in ``text``, or ``None``."""
    for line in text.splitlines():
        match = _VERSION_LINE.match(line)
        if match is None:
            continue
        value = _strip_inline_comment(match.group(1)).strip().strip("\"'").strip()
        if _VERSION_TOKEN.fullmatch(value):
            return value
    return None


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
            except (OSError, UnicodeDecodeError):
                continue
            version = _version_from_text(text)
            if version is not None:
                return version
    return _FALLBACK_VERSION


SDK_VERSION: str = _load_version()
