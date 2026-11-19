from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


@dataclass(slots=True)
class Checkpoint:
    name: str
    at_ms: int
    attrs: dict[str, Any] = field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        # Reserved fields remain authoritative even if attrs carries the same keys.
        result: dict[str, Any] = dict(self.attrs)
        result["name"] = self.name
        result["at_ms"] = self.at_ms
        return result
        # Reserved checkpoint fields must win over user attrs so that arbitrary
        # metadata can never impersonate or replace the real name/timestamp.
        return {**self.attrs, "name": self.name, "at_ms": self.at_ms}
