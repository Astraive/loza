from __future__ import annotations

from datetime import datetime, timedelta, timezone

import loza
from loza.core.checkpoint import Checkpoint


def _context() -> loza.EventContext:
    start = datetime(2024, 1, 1, tzinfo=timezone.utc)
    return loza.EventContext(
        service="checkout",
        params=loza.Params(event="checkout.run"),
        started_at=start,
        finished_at=start + timedelta(milliseconds=42),
    )


def test_checkpoint_reserved_fields_cannot_be_overridden_by_attrs() -> None:
    ctx = _context()

    ctx.checkpoint("validated", at_ms=999_999, stage="one")

    checkpoint = ctx.checkpoints[0]
    assert checkpoint["name"] == "validated"
    assert checkpoint["at_ms"] == 42
    assert checkpoint["stage"] == "one"


def test_checkpoint_public_api_preserves_reserved_fields() -> None:
    ctx = _context()

    loza.checkpoint(ctx, "charged", loza.String("provider", "stripe"), at_ms=123)

    checkpoint = ctx.checkpoints[0]
    assert checkpoint["name"] == "charged"
    assert checkpoint["at_ms"] == 42
    assert checkpoint["provider"] == "stripe"


def test_checkpoint_to_dict_reserved_fields_cannot_be_overridden_by_attrs() -> None:
    cp = Checkpoint(name="validated", at_ms=7, attrs={"name": "fake", "at_ms": 999, "stage": "one"})

    result = cp.to_dict()

    assert result["name"] == "validated"
    assert result["at_ms"] == 7
    assert result["stage"] == "one"
