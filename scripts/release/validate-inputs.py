#!/usr/bin/env python3
"""Validate workflow inputs before they are used by release jobs.

Inputs are read from environment variables, never interpolated into shell or
Python source. Set any of the following variables (only variables that are
present are validated):

  RELEASE_VERSION     - semantic version X.Y.Z (no leading 'v', no suffix)
  RELEASE_COMPONENT   - a single component key from release.yaml
  RELEASE_COMPONENTS  - comma-separated component keys from release.yaml
  RELEASE_DRY_RUN     - optional boolean, 'true' or 'false'

The process exits non-zero with a clear message when any value is invalid so
that a workflow fails closed before an input reaches a command.
"""

from __future__ import annotations

import os
import re
import sys
from pathlib import Path
from typing import Callable

ROOT = Path(__file__).resolve().parents[2]
SEMVER_RE = re.compile(r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$")


def known_components() -> set[str]:
    """Read the top-level component keys from release.yaml without executing code."""
    registry = ROOT / "release.yaml"
    components: set[str] = set()
    in_components = False
    for raw in registry.read_text(encoding="utf-8").splitlines():
        if not raw.strip() or raw.lstrip().startswith("#"):
            continue
        if not raw[:1].isspace():
            in_components = raw.strip() == "components:"
            continue
        if in_components and ":" in raw:
            key = raw.strip().split(":", 1)[0].strip()
            if key:
                components.add(key)
    return components


def validate_version(value: str) -> str:
    if not SEMVER_RE.fullmatch(value):
        raise ValueError("version must be X.Y.Z (no leading 'v' and no suffix)")
    return value


def validate_component(value: str, components: set[str] | None = None) -> str:
    allowed = components if components is not None else known_components()
    if value not in allowed:
        raise ValueError(f"unknown component: {value!r}")
    return value


def validate_components(value: str, components: set[str] | None = None) -> list[str]:
    selected = [item.strip() for item in value.split(",")]
    if not selected or any(not item for item in selected):
        raise ValueError("components must be a non-empty comma-separated list")
    for item in selected:
        validate_component(item, components)
    return selected


def validate_dry_run(value: str) -> str:
    if value not in {"true", "false"}:
        raise ValueError("dry_run must be 'true' or 'false'")
    return value


def main() -> int:
    components = known_components()
    checks: list[tuple[str, Callable[[str], object]]] = [
        ("RELEASE_VERSION", validate_version),
        ("RELEASE_COMPONENT", lambda value: validate_component(value, components)),
        ("RELEASE_COMPONENTS", lambda value: validate_components(value, components)),
        ("RELEASE_DRY_RUN", validate_dry_run),
    ]
    checked = 0
    for name, check in checks:
        if name not in os.environ:
            continue
        checked += 1
        try:
            check(os.environ[name])
        except ValueError as exc:
            print(f"invalid {name}: {exc}; refusing to continue", file=sys.stderr)
            return 1
    if checked == 0:
        print("validate-inputs.py: no RELEASE_* inputs are set; nothing to validate", file=sys.stderr)
        return 2
    print(f"validated {checked} workflow input(s)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
