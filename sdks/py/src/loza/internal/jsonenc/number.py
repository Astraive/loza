from __future__ import annotations

import math
from decimal import Decimal
from typing import Any


def safe_number(value: Any):
    if isinstance(value, bool):
        return value
    if isinstance(value, int):
        return value
    if isinstance(value, float):
        return value if math.isfinite(value) else None
    if isinstance(value, Decimal):
        if not value.is_finite():
            return None
        result = float(value)
        return result if math.isfinite(result) else None
    return None


def clamp_int(value: int, minimum: int | None = None, maximum: int | None = None) -> int:
    if minimum is not None:
        value = max(minimum, value)
    if maximum is not None:
        value = min(maximum, value)
    return value


def duration_ms(seconds: float) -> int:
    if not math.isfinite(seconds):
        return 0
    return max(0, int(round(seconds * 1000, 6)))
