"""Regression tests for the benchmark train/eval incident split.

Held-out eval incident events (their signals in particular) must never appear
in the training partition, otherwise the benchmark measures reconstruction of
incidents the engine was already trained on.
"""

from __future__ import annotations

import os
import sys
from datetime import datetime, timedelta, timezone

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from bench.dataset import (  # noqa: E402
    N_DAYS,
    generate_dataset,
)

BASE_TIME = datetime(2026, 5, 1, 0, 0, 0, tzinfo=timezone.utc)
SPLIT_TIME = (BASE_TIME + timedelta(days=N_DAYS - 2)).isoformat()


def _identity(event: dict) -> tuple:
    """Event identity that does not rely on incident_id alone."""
    return (
        event.get("kind"),
        event.get("ts"),
        event.get("incident_id"),
        event.get("service"),
        event.get("trigger"),
    )


def test_eval_signal_ids_never_appear_in_train_events():
    """No eval ground-truth signal ID may be present in the training set."""
    for seed in (42, 0, 1, 7, 1234):
        dataset = generate_dataset(seed=seed)
        train_ids = {
            e["incident_id"] for e in dataset["train_events"] if "incident_id" in e
        }
        eval_ids = {s["incident_id"] for s in dataset["eval_signals"]}
        assert not (train_ids & eval_ids), (
            f"seed={seed}: eval signal IDs leaked into train_events: "
            f"{sorted(train_ids & eval_ids)}"
        )


def test_eval_signals_are_only_in_eval_events():
    """Every eval signal must live in eval_events, on/after the split."""
    dataset = generate_dataset(seed=42)
    train_identities = {_identity(e) for e in dataset["train_events"]}
    eval_identities = {_identity(e) for e in dataset["eval_events"]}

    for signal in dataset["eval_signals"]:
        assert _identity(signal) in eval_identities, (
            f"eval signal {signal['incident_id']} missing from eval_events"
        )
        assert _identity(signal) not in train_identities, (
            f"eval signal {signal['incident_id']} leaked into train_events"
        )
        assert signal["ts"] >= SPLIT_TIME, (
            f"eval signal {signal['incident_id']} dated before split_time"
        )


def test_eval_incident_events_all_fall_on_eval_side_of_split():
    """All events belonging to a held-out incident are after split_time."""
    dataset = generate_dataset(seed=42)
    eval_ids = {s["incident_id"] for s in dataset["eval_signals"]}

    eval_incident_events = [
        e for e in dataset["eval_events"]
        if e.get("incident_id") in eval_ids
    ]
    train_incident_events = [
        e for e in dataset["train_events"]
        if e.get("incident_id") in eval_ids
    ]
    assert eval_incident_events, "expected eval incident events in eval_events"
    assert not train_incident_events, (
        "eval incident events found in train_events: "
        f"{[e['incident_id'] for e in train_incident_events]}"
    )
    assert all(e["ts"] >= SPLIT_TIME for e in eval_incident_events)


def test_train_incidents_are_confined_to_train_partition():
    """Train incident IDs are distinct from eval IDs and stay before split."""
    dataset = generate_dataset(seed=42)
    train_ids = {
        e["incident_id"] for e in dataset["train_events"] if "incident_id" in e
    }
    eval_ids = {s["incident_id"] for s in dataset["eval_signals"]}
    assert train_ids.isdisjoint(eval_ids)
    assert all(e["ts"] < SPLIT_TIME for e in dataset["train_events"]
               if "incident_id" in e)
