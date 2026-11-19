"""Regression tests for the benchmark summary generator.

`datetime.utcnow()` is deprecated as of Python 3.12 and scheduled for
removal. These tests simulate a future interpreter where the API is gone
and assert that a UTC timestamp can still be generated.
"""

import importlib.util
from datetime import datetime, timezone
from pathlib import Path

import pytest

SUMMARY_PATH = Path(__file__).resolve().parents[1] / "lib" / "summary.py"


def _load_summary():
    """Load bench/lib/summary.py as a standalone module (lib is not a package)."""
    spec = importlib.util.spec_from_file_location("bench_summary", SUMMARY_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class _UtcNowRemoved:
    """A datetime stand-in modelling an interpreter with utcnow() removed."""

    @staticmethod
    def utcnow():
        raise AttributeError("datetime.datetime has no attribute 'utcnow'")

    @staticmethod
    def now(tz=None):
        # The fix must request the UTC timezone explicitly.
        assert tz is timezone.utc
        return datetime(2026, 1, 2, 3, 4, 5, tzinfo=tz)


def test_render_markdown_works_when_utcnow_is_removed():
    """A future interpreter without utcnow() must not break summary output."""
    summary = _load_summary()
    original = summary.datetime
    summary.datetime = _UtcNowRemoved
    try:
        output = summary.render_markdown([])
    finally:
        summary.datetime = original

    assert "Generated: 2026-01-02 03:04:05 UTC" in output
    assert "No benchmark results found." in output


def test_summary_source_does_not_call_utcnow():
    """Guard against reintroducing the deprecated API in this module."""
    assert ".utcnow(" not in SUMMARY_PATH.read_text()


def test_render_markdown_emits_utc_timestamp_with_real_clock():
    """With the real clock the header is a well-formed UTC timestamp."""
    summary = _load_summary()
    output = summary.render_markdown([])
    line = next(line for line in output.splitlines() if line.startswith("Generated: "))
    stamp = line.removeprefix("Generated: ")
    parsed = datetime.strptime(stamp, "%Y-%m-%d %H:%M:%S UTC")
    assert parsed.year >= 2026
