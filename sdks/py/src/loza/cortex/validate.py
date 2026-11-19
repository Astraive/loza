"""Cortex validation stub - lightweight SDK interface.

Full validation logic lives in the loza-cortex package.
"""

from __future__ import annotations

from typing import Any


def validate_cortex_event(event: dict[str, Any], strict: bool = False) -> list[str]:
    """Validate an event for cortex ingestion (stub).

    Cortex events are identified by ``incident_id`` (see ``CortexEventSchema``
    and the cortex API models). ``event_id`` is accepted as a legacy alias so
    existing callers that still use it are not broken; ``incident_id`` is the
    documented, schema-aligned field.
    """
    errors: list[str] = []
    if not (event.get("incident_id") or event.get("event_id")):
        errors.append("missing incident_id")
    return errors


def is_valid_cortex_event(event: dict[str, Any]) -> bool:
    return len(validate_cortex_event(event)) == 0
