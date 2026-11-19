from __future__ import annotations

from datetime import datetime, timedelta, timezone

from loza.core.event import EventContext, Params
from loza.core.schema import EventView, OTelLogSchema


def _encode(started_at: datetime) -> dict[str, object]:
    event = EventContext(
        service="checkout",
        params=Params(event="payment.completed", message="paid", level="info"),
        started_at=started_at,
    )
    return OTelLogSchema().encode(EventView(event))


def test_time_unix_nano_is_integer_nanoseconds_not_iso_string() -> None:
    started_at = datetime(2024, 1, 1, 0, 0, 0, 123456, tzinfo=timezone.utc)
    encoded = _encode(started_at)

    value = encoded["time_unix_nano"]
    assert isinstance(value, int), f"time_unix_nano must be an int, got {type(value).__name__}: {value!r}"
    expected = int(started_at.timestamp()) * 1_000_000_000 + started_at.microsecond * 1_000
    assert value == expected


def test_time_unix_nano_preserves_microsecond_precision_across_offsets() -> None:
    # 2024-01-01T00:00:00.000007Z expressed with a non-UTC offset.
    started_at = datetime(2024, 1, 1, 2, 0, 0, 7, tzinfo=timezone(timedelta(hours=2)))
    encoded = _encode(started_at)

    value = encoded["time_unix_nano"]
    assert isinstance(value, int)
    assert value == 1704067200000007000


def test_time_unix_nano_handles_unparseable_timestamp() -> None:
    class StubView:
        def to_dict(self) -> dict[str, object]:
            return {"timestamp": "not-a-timestamp", "level": "info", "message": "paid"}

    encoded = OTelLogSchema().encode(StubView())  # type: ignore[arg-type]
    assert encoded["time_unix_nano"] is None
