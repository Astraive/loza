"""Regression tests for the deterministic benchmark dataset generator."""

from __future__ import annotations

import random
from datetime import datetime, timezone

from bench.dataset import _generate_incidents


def _ids(incidents: list[dict]) -> set[str]:
    return {incident["incident_id"] for incident in incidents}


def test_train_and_eval_incident_ids_do_not_overlap() -> None:
    """Train and eval incidents must never share an incident_id.

    Previously both phases generated IDs from ``INC-100`` because the ID was
    derived from a per-call local list, so train INC-100 and eval INC-100 were
    indistinguishable to consumers. The phase is now part of the ID.
    """
    base_time = datetime(2026, 5, 1, tzinfo=timezone.utc)
    rng = random.Random(42)
    train = _generate_incidents(rng, base_time, 24, [], "train")
    eval_incidents = _generate_incidents(rng, base_time, 10, [], "eval")

    train_ids = _ids(train)
    eval_ids = _ids(eval_incidents)

    assert train_ids, "expected train incidents"
    assert eval_ids, "expected eval incidents"
    assert train_ids.isdisjoint(eval_ids), (
        f"train/eval incident IDs overlap: {sorted(train_ids & eval_ids)}"
    )


def test_incident_ids_encode_phase_and_are_unique() -> None:
    """Every generated incident ID is phase-prefixed and globally unique."""
    base_time = datetime(2026, 5, 1, tzinfo=timezone.utc)
    rng = random.Random(42)
    train = _generate_incidents(rng, base_time, 24, [], "train")
    eval_incidents = _generate_incidents(rng, base_time, 10, [], "eval")

    train_ids = _ids(train)
    eval_ids = _ids(eval_incidents)

    assert len(train_ids) == 24, "train incident IDs must be unique within the phase"
    assert len(eval_ids) == 10, "eval incident IDs must be unique within the phase"
    assert all(incident_id.startswith("INC-train-") for incident_id in train_ids)
    assert all(incident_id.startswith("INC-eval-") for incident_id in eval_ids)
