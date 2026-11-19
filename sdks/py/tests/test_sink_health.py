from __future__ import annotations

from loza.sinks import Health, health


class _HealthySink:
    def health(self) -> bool:
        return True


class _UnhealthySink:
    def health(self) -> bool:
        return False


class _UncheckedSink:
    """Sink that exposes no health check at all."""


class _NonCallableHealthSink:
    health = "yes"


def test_health_returns_sink_verdict_when_check_is_available() -> None:
    assert health(_HealthySink()) is True
    assert health(_UnhealthySink()) is False


def test_health_returns_none_when_check_is_unavailable() -> None:
    # Regression: an absent/non-callable health check must not be reported as
    # verified healthy; callers need None to distinguish unverified from healthy.
    assert health(_UncheckedSink()) is None
    assert health(_NonCallableHealthSink()) is None


def test_health_pascalcase_alias_matches_health() -> None:
    assert Health(_UncheckedSink()) is None
    assert Health(_HealthySink()) is True
