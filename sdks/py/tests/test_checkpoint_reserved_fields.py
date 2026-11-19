from __future__ import annotations

from datetime import datetime, timedelta, timezone
"""
Regression tests for reserved checkpoint fields.

The ``name`` and ``at_ms`` checkpoint fields are structural/reserved fields.
Arbitrary user-supplied attrs must never be able to silently replace them,
mirroring the protection LOZA already applies to canonical event fields.
"""

from __future__ import annotations

import json

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
def test_to_dict_reserved_fields_win_over_user_attrs() -> None:
    """User attrs named like reserved fields cannot overwrite name/at_ms."""
    checkpoint = Checkpoint(
        name="db.started",
        at_ms=1234,
        attrs={"name": "forged", "at_ms": 9999, "phase": "db"},
    )

    result = checkpoint.to_dict()

    assert result["name"] == "db.started"
    assert result["at_ms"] == 1234
    # Non-reserved attrs are still preserved.
    assert result["phase"] == "db"


def test_to_dict_reserved_attrs_are_dropped() -> None:
    """Reserved keys in attrs are ignored rather than emitted twice/overridden."""
    checkpoint = Checkpoint(
        name="job.done",
        at_ms=7,
        attrs={"name": "evil", "at_ms": -1},
    )

    result = checkpoint.to_dict()

    assert result == {"name": "job.done", "at_ms": 7}


def test_event_context_checkpoint_reserved_fields_win() -> None:
    """The live EventContext.checkpoint path cannot have at_ms overridden by attrs."""
    logger = loza.New(loza.Test("checkpoint-reserved"))
    ctx = logger.start_event(loza.Params(event="job.run"))

    loza.Checkpoint(ctx, "db.started", loza.String("phase", "db"), **{"at_ms": 9999})
    logger.finish(ctx, "success")
    payload = json.loads(logger.emit(ctx))

    checkpoint = payload["checkpoints"][0]
    assert checkpoint["name"] == "db.started"
    # The forged value must not override the real elapsed time.
    assert checkpoint["at_ms"] != 9999
    assert checkpoint["phase"] == "db"
