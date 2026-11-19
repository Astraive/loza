"""Regression tests for tools/generate-parity-report.py status handling.

These tests load the standalone script by path (it is not an importable
package) and exercise the status-to-cell/count mapping used by the generated
report.  They reproduce MASS-0866 / issue #956: equivalent truthy spellings
of a status were printed verbatim but silently excluded from the Coverage
column because the membership check was case- and whitespace-sensitive.
"""

from __future__ import annotations

import importlib.util
from pathlib import Path

_SCRIPT = Path(__file__).resolve().parent / "generate-parity-report.py"
_spec = importlib.util.spec_from_file_location("generate_parity_report", _SCRIPT)
assert _spec is not None and _spec.loader is not None
gpr = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(gpr)


def _manifests_with_status(status: object) -> dict[str, dict | None]:
    """Build manifests where Go has one feature with ``status`` and others are N/A."""
    manifests: dict[str, dict | None] = {sdk: None for sdk in gpr.SDKS}
    manifests["go"] = {"features": [{"name": "feature_a", "status": status}]}
    return manifests


def test_truthy_status_spellings_are_all_counted_as_supported() -> None:
    """Every equivalent spelling of a supported status must be counted."""
    spellings = [
        "Y",
        "y",
        "yes",
        "YES",
        "Yes",
        " true ",
        "TRUE",
        "supported",
        "SuPpOrTeD",
        " complete ",
        "COMPLETE",
        "done",
        "DONE",
    ]
    for spelling in spellings:
        report = gpr.generate_report(_manifests_with_status(spelling), ["feature_a"])
        assert "| feature_a | Y | -- | -- | -- |" in report, spelling
        assert "| Go | 1 | 1 | 100% |" in report, spelling


def test_status_in_feature_key_form_is_also_normalized() -> None:
    """The ``{feature: {status: ...}}`` manifest shape must behave the same."""
    manifests: dict[str, dict | None] = {sdk: None for sdk in gpr.SDKS}
    manifests["go"] = {"feature_a": {"status": " YES "}}
    report = gpr.generate_report(manifests, ["feature_a"])
    assert "| feature_a | Y | -- | -- | -- |" in report
    assert "| Go | 1 | 1 | 100% |" in report


def test_boolean_true_is_still_supported() -> None:
    """Booleans keep their existing canonical Y/N mapping."""
    report = gpr.generate_report(_manifests_with_status(True), ["feature_a"])
    assert "| feature_a | Y | -- | -- | -- |" in report
    assert "| Go | 1 | 1 | 100% |" in report

    report = gpr.generate_report(_manifests_with_status(False), ["feature_a"])
    assert "| feature_a | N | -- | -- | -- |" in report
    assert "| Go | 0 | 1 | 0% |" in report


def test_not_applicable_and_negative_statuses_are_not_counted() -> None:
    """Normalization must not turn N/A or unsupported statuses into Y."""
    for status, cell in [("N/A", "--"), (" n/a ", "--"), ("na", "--"), ("N", "N"), ("no", "no"), ("partial", "partial")]:
        report = gpr.generate_report(_manifests_with_status(status), ["feature_a"])
        assert f"| feature_a | {cell} | -- | -- | -- |" in report, status
        assert "| Go | 0 | 1 | 0% |" in report, status
