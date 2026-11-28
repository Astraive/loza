from __future__ import annotations

from collections.abc import Mapping
from typing import Any

from ... import Error, Info, Warn


class LoguruSink:
    def __call__(self, message: Any) -> None:
        record = getattr(message, "record", {})
        text = str(getattr(message, "message", message)).strip()
        raw_level = record.get("level", "INFO") if isinstance(record, Mapping) else "INFO"
        if isinstance(raw_level, Mapping):
            raw_level = raw_level.get("name", "INFO")
        else:
            raw_level = getattr(raw_level, "name", raw_level)
        level = str(raw_level).lower()
        if level in {"error", "critical"}:
            Error(text)
        elif level == "warning":
            Warn(text)
        else:
            Info(text)


def bind_loza(logger):
    """Attach the Loza sink to a Loguru logger and return the logger.

    This is a convenience wrapper around ``logger.add(sink())``. Calling it
    actually wires Loguru records into Loza; it is no longer a no-op.
    """
    logger.add(sink())
    return logger


def sink() -> LoguruSink:
    return LoguruSink()
