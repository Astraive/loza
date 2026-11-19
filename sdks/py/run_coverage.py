"""Run the deterministic SDK coverage gate for this package."""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

PACKAGE_DIR = Path(__file__).resolve().parent
SOURCE_DIR = PACKAGE_DIR / "src" / "loza"


def main() -> int:
    commands = (
        [sys.executable, "-m", "coverage", "erase"],
        [
            sys.executable,
            "-m",
            "coverage",
            "run",
            "--branch",
            f"--source={SOURCE_DIR}",
            "-m",
            "pytest",
            "--ignore=tests/test_e2e_live.py",
        ],
        [
            sys.executable,
            "-m",
            "coverage",
            "html",
        ],
        [
            sys.executable,
            "-m",
            "coverage",
            "json",
        ],
        [
            sys.executable,
            "-m",
            "coverage",
            "report",
            "--fail-under=75",
            "--show-missing",
        ],
    )
    for command in commands:
        completed = subprocess.run(command, check=False, cwd=PACKAGE_DIR)
        if completed.returncode:
            return completed.returncode
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
