"""Regression tests for topology rename mutations in the benchmark dataset."""

from __future__ import annotations

import sys
from pathlib import Path

# ``bench`` is a top-level package in sdks/py (not part of the installed
# ``loza`` distribution), so make it importable when running pytest from here.
_PY_ROOT = Path(__file__).resolve().parents[1]
if str(_PY_ROOT) not in sys.path:
    sys.path.insert(0, str(_PY_ROOT))

from bench.dataset import generate_dataset  # noqa: E402


def test_rename_mutations_are_never_noops_seed_7() -> None:
    """The reported seed-7 reproduction must yield only real renames."""
    dataset = generate_dataset(seed=7)
    mutations = dataset["topology_mutations"]

    assert mutations
    assert all(m["from"] != m["to"] for m in mutations), "found a rename mutation with identical 'from' and 'to'"


def test_all_rename_mutations_are_real_renames_across_seeds() -> None:
    """No seed may produce a rename whose source and target are identical."""
    for seed in range(20):
        dataset = generate_dataset(seed=seed)
        mutations = dataset["topology_mutations"]
        assert mutations
        noops = [m for m in mutations if m["from"] == m["to"]]
        assert noops == [], f"seed {seed} produced no-op renames: {noops}"


def test_service_without_svc_suffix_gets_a_distinct_name() -> None:
    """Odd-index branch falls back to a real rename for names lacking '-svc'."""
    dataset = generate_dataset(seed=7)
    checkout = [m for m in dataset["topology_mutations"] if m["from"] == "checkout-api"]

    assert checkout
    for mutation in checkout:
        assert mutation["to"] != "checkout-api"
        assert mutation["to"] == "checkout-api-v2"
