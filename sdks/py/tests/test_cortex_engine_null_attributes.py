"""Regression tests for null ``attributes`` in causal-chain entries."""

from loza.cortex.engine import Engine


def _edge(resp: dict):
    return Engine("https://cortex")._map_response(resp).causal_chain[0]


def test_null_attributes_defaults_without_crashing() -> None:
    """A JSON ``null`` attributes value must be treated as an empty mapping."""
    edge = _edge(
        {
            "incident_id": "i",
            "causal_chain": [
                {
                    "event_id": "e",
                    "description": "because",
                    "attributes": None,
                }
            ],
        }
    )

    assert edge.cause_id == ""
    assert edge.effect_id == "e"
    assert edge.evidence == "because"
    assert edge.confidence == 0.5


def test_missing_attributes_still_default() -> None:
    """A missing attributes key keeps the historical default behavior."""
    edge = _edge({"incident_id": "i", "causal_chain": [{"event_id": "e"}]})

    assert edge.cause_id == ""
    assert edge.confidence == 0.5


def test_populated_attributes_are_mapped() -> None:
    """Valid attribute mappings remain unchanged by the normalization."""
    edge = _edge(
        {
            "incident_id": "i",
            "causal_chain": [
                {
                    "event_id": "e",
                    "attributes": {"cause_id": "c", "confidence": 0.9},
                }
            ],
        }
    )

    assert edge.cause_id == "c"
    assert edge.confidence == 0.9
