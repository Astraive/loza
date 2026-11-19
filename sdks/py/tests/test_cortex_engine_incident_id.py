"""Regression tests for incident_id resolution in the Cortex engine adapter."""

from __future__ import annotations

from loza.cortex.engine import Engine


def _captured_reconstruct_body(monkeypatch, signal: dict) -> dict:
    """Call reconstruct_context and return the request body posted to /reconstruct."""
    engine = Engine("https://cortex")
    captured: dict = {}

    def fake_post(path: str, body: dict) -> dict:
        captured["path"] = path
        captured["body"] = body
        return {}

    monkeypatch.setattr(engine, "_post", fake_post)
    engine.reconstruct_context(signal)
    return captured["body"]


def test_explicit_none_incident_id_falls_back_to_signal_id(monkeypatch) -> None:
    """An explicit None incident_id must not suppress the signal_id fallback."""
    body = _captured_reconstruct_body(
        monkeypatch,
        {"incident_id": None, "signal_id": "SIG-42"},
    )
    assert body["incident_id"] == "SIG-42"


def test_empty_incident_id_falls_back_to_signal_id(monkeypatch) -> None:
    """An empty incident_id must also fall back to signal_id."""
    body = _captured_reconstruct_body(
        monkeypatch,
        {"incident_id": "", "signal_id": "SIG-42"},
    )
    assert body["incident_id"] == "SIG-42"


def test_absent_incident_id_uses_signal_id(monkeypatch) -> None:
    """A missing incident_id keeps the existing signal_id fallback behaviour."""
    body = _captured_reconstruct_body(monkeypatch, {"signal_id": "SIG-42"})
    assert body["incident_id"] == "SIG-42"


def test_present_incident_id_is_preferred(monkeypatch) -> None:
    """A truthy incident_id still takes precedence over signal_id."""
    body = _captured_reconstruct_body(
        monkeypatch,
        {"incident_id": "INC-7", "signal_id": "SIG-42"},
    )
    assert body["incident_id"] == "INC-7"


def test_missing_identifiers_default_to_empty_string(monkeypatch) -> None:
    """With neither identifier present, incident_id defaults to an empty string."""
    body = _captured_reconstruct_body(monkeypatch, {})
    assert body["incident_id"] == ""
