"""Regression tests for Prometheus label-value escaping (issue #234)."""

from __future__ import annotations

import pytest

from loza import metrics as legacy_metrics
from loza.core import metric


@pytest.mark.parametrize("module", [metric, legacy_metrics])
def test_escape_label_value_escapes_carriage_return(module) -> None:
    assert module._escape_label_value("a\rb") == "a\\rb"
    # Existing escapes remain intact and introduced backslashes are not re-escaped.
    assert module._escape_label_value("a\\b\nc\r\n\"d") == "a\\\\b\\nc\\r\\n\\\"d"


@pytest.mark.parametrize("module", [metric, legacy_metrics])
def test_dropped_reason_carriage_return_is_escaped_in_exposition(module) -> None:
    collector = module.MetricsCollector("checkout")
    collector.on_event_dropped("bad\rreason")

    rendered = collector.render_prometheus()

    # A literal CR must never be written into the text exposition format.
    assert "\r" not in rendered
    assert 'checkout_events_dropped_total{reason="bad\\rreason"} 1' in rendered
