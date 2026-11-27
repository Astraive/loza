"""Regression coverage for honest Rust benchmark status reporting."""

import os
import shlex
import subprocess
import tempfile
from pathlib import Path


RUN_ALL = Path(__file__).resolve().parents[1] / "run-all.sh"


def test_rust_suite_skips_without_invoking_cargo_or_writing_a_result():
    source = RUN_ALL.read_text()
    start = source.index("# 6. Rust SDK Benchmarks")
    end = source.index("# 7. JavaScript SDK Benchmarks", start)
    rust_branch = source[start:end]

    with tempfile.TemporaryDirectory() as temp_dir:
        root = Path(temp_dir)
        bin_dir = root / "bin"
        bin_dir.mkdir()
        results_dir = root / "results"
        results_dir.mkdir()
        cargo_marker = root / "cargo-was-invoked"
        cargo = bin_dir / "cargo"
        cargo.write_text(
            "#!/bin/sh\nprintf invoked > "
            + shlex.quote(str(cargo_marker))
            + "\nexit 73\n"
        )
        cargo.chmod(0o755)
        prelude = (
            "set -euo pipefail\n"
            f"REPO_ROOT={shlex.quote(str(root))}\n"
            f"RESULTS_DIR={shlex.quote(str(results_dir))}\n"
            "TIMESTAMP=regression\nPASSED=0\nFAILED=0\nSKIPPED=0\n"
        )

        result = subprocess.run(
            ["/bin/bash", "-c", prelude + rust_branch + "printf 'Passed: %s\\nSkipped: %s\\n' \"$PASSED\" \"$SKIPPED\"\n"],
            env={**os.environ, "PATH": str(bin_dir)},
            capture_output=True,
            text=True,
            check=False,
        )

        assert result.returncode == 0, result.stderr
        assert "SKIP: sdk-rs" in result.stdout
        assert "PASS: sdk-rs" not in result.stdout
        assert "Passed: 0" in result.stdout
        assert "Skipped: 1" in result.stdout
        assert not cargo_marker.exists()
        assert list(results_dir.iterdir()) == []
