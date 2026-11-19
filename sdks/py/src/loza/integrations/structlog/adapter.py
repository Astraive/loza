from __future__ import annotations

from typing import Any

from ... import Info


class StructlogProcessor:
    def __init__(self, emit_immediate: bool = True) -> None:
        self.emit_immediate = emit_immediate

    def __call__(self, logger, method_name: str, event_dict: dict[str, Any]):
        if self.emit_immediate:
            message = str(event_dict.get("event", method_name))
            Info(message, **{k: v for k, v in event_dict.items() if k != "event"})
        return event_dict


def bind_loza(logger):
    """Attach the Loza processor to structlog and return ``logger``.

    Unlike Loguru, structlog has no per-logger ``add`` hook: processors are
    installed on the active configuration with ``structlog.configure``. This
    helper therefore prepends a :class:`StructlogProcessor` to the configured
    processor chain so log records are actually mirrored into Loza. The call is
    idempotent (existing Loza processors are not duplicated) and the original
    logger is returned for chaining/drop-in compatibility.
    """
    import structlog

    processors = list(structlog.get_config().get("processors") or ())
    if not any(isinstance(item, StructlogProcessor) for item in processors):
        structlog.configure(processors=[StructlogProcessor(), *processors])
    return logger


def processor(**kwargs) -> StructlogProcessor:
    return StructlogProcessor(**kwargs)
