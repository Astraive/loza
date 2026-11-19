from __future__ import annotations

# This module acts as a compatibility layer and re-exports from the authoritative loza-spec contract.
# Do NOT add new constants here—all definitions come from loza-spec/generated/python/loza_contract.py

import sys
import logging
import importlib.machinery
import importlib.util
from pathlib import Path

logger = logging.getLogger(__name__)


def _validate_flexible_json_bytes_unavailable(raw: bytes, strict: bool) -> None:
    """Callable fallback for backup contracts that omit ``validate_flexible_json_bytes``.

    The symbol is part of this module's public ``__all__``. Resolving it to ``None``
    would leave an exported-but-uncallable name that raises ``TypeError`` at call
    time. Keeping a real callable preserves the API and fails with an actionable
    error instead.
    """
    raise NotImplementedError(
        "validate_flexible_json_bytes is not available in the bundled backup contract; "
        "install loza-spec to enable flexible JSON byte validation."
    )

# Try importing from loza-spec (primary path - when installed as dependency)
try:
    from loza_contract import (
        LOZA_SPEC_VERSION,
        LOZA_INGEST_API_VERSION,
        LOZA_EVENT_VERSION,
        MAX_EVENT_BYTES,
        ALLOWED_TOP_LEVEL_FIELDS,
        CANONICAL_FIELDS,
        ALLOWED_KINDS,
        ALLOWED_LEVELS,
        ALLOWED_OUTCOMES,
        ALLOWED_PARTIAL_REASONS,
        ALLOWED_EVENT_STATES,
        CollectorAck,
        CollectorError,
        CollectorResponse,
        parse_collector_response,
        looks_like_loza_event,
        normalize_event_aliases,
        build_ingest_envelope,
        validate_event_payload,
        validate_event_payload_detailed,
        validate_flexible_json_bytes,
        ValidationError,
        ValidationErrors,
    )
except ImportError:
    # Only a genuinely missing/incomplete dependency may trigger fallback. Any other
    # import-time failure (NameError, bad constant, validation error, ...) must
    # propagate instead of silently running against a stale contract.
    logger.warning(
        "Could not import the authoritative loza contract; falling back to the "
        "monorepo/backup contract, which may omit spec/security fixes.",
        exc_info=True,
    )
    # Fallback: try importing from sibling loza-spec directory (for development/monorepo)
    try:
        spec_dir = Path(__file__).parent.parent.parent.parent.parent.parent / "spec" / "generated" / "python"
        if spec_dir.exists():
            sys.path.insert(0, str(spec_dir))
            from loza_contract import (
                LOZA_SPEC_VERSION,
                LOZA_INGEST_API_VERSION,
                LOZA_EVENT_VERSION,
                MAX_EVENT_BYTES,
                ALLOWED_TOP_LEVEL_FIELDS,
                CANONICAL_FIELDS,
                ALLOWED_KINDS,
                ALLOWED_LEVELS,
                ALLOWED_OUTCOMES,
                ALLOWED_PARTIAL_REASONS,
                ALLOWED_EVENT_STATES,
                CollectorAck,
                CollectorError,
                CollectorResponse,
                parse_collector_response,
                looks_like_loza_event,
                normalize_event_aliases,
                build_ingest_envelope,
                validate_event_payload,
                validate_event_payload_detailed,
                validate_flexible_json_bytes,
                ValidationError,
                ValidationErrors,
            )
        else:
            raise ImportError(f"loza-spec not found at {spec_dir}")
    except ImportError as e:
        logger.warning(
            "Could not import the loza contract from the monorepo path; falling "
            "back to the backup contract, which may omit spec/security fixes.",
            exc_info=True,
        )
except Exception:
    # Fallback: load the contract from a sibling loza-spec checkout in the
    # monorepo (development only). Load it directly from its file path instead
    # of inserting the directory into sys.path: mutating the process-wide
    # module search path on import lets this development copy shadow other
    # installed modules and makes behavior depend on import order.
    try:
        spec_dir = Path(__file__).parent.parent.parent.parent.parent.parent / "spec" / "generated" / "python"
        contract_path = spec_dir / "loza_contract.py"
        if not contract_path.exists():
            raise ImportError(f"loza-spec not found at {contract_path}")
        loader = importlib.machinery.SourceFileLoader("loza_contract_from_spec", str(contract_path))
        spec = importlib.util.spec_from_loader(loader.name, loader)
        if spec is None:
            raise ImportError(f"Cannot load contract module from {contract_path}")
        contract = importlib.util.module_from_spec(spec)
        sys.modules[loader.name] = contract
        loader.exec_module(contract)

        LOZA_SPEC_VERSION = contract.LOZA_SPEC_VERSION
        LOZA_INGEST_API_VERSION = contract.LOZA_INGEST_API_VERSION
        LOZA_EVENT_VERSION = contract.LOZA_EVENT_VERSION
        MAX_EVENT_BYTES = contract.MAX_EVENT_BYTES
        ALLOWED_TOP_LEVEL_FIELDS = contract.ALLOWED_TOP_LEVEL_FIELDS
        CANONICAL_FIELDS = contract.CANONICAL_FIELDS
        ALLOWED_KINDS = contract.ALLOWED_KINDS
        ALLOWED_LEVELS = contract.ALLOWED_LEVELS
        ALLOWED_OUTCOMES = contract.ALLOWED_OUTCOMES
        ALLOWED_PARTIAL_REASONS = contract.ALLOWED_PARTIAL_REASONS
        ALLOWED_EVENT_STATES = contract.ALLOWED_EVENT_STATES
        CollectorAck = contract.CollectorAck
        CollectorError = contract.CollectorError
        CollectorResponse = contract.CollectorResponse
        parse_collector_response = contract.parse_collector_response
        looks_like_loza_event = contract.looks_like_loza_event
        normalize_event_aliases = contract.normalize_event_aliases
        build_ingest_envelope = contract.build_ingest_envelope
        validate_event_payload = contract.validate_event_payload
        validate_event_payload_detailed = contract.validate_event_payload_detailed
        validate_flexible_json_bytes = contract.validate_flexible_json_bytes
        ValidationError = contract.ValidationError
        ValidationErrors = contract.ValidationErrors
    except Exception as e:
        backup_path = Path(__file__).with_name("spec_contract.py.bak")
        if not backup_path.exists():
            raise ImportError(
                "Cannot import loza contract. Install loza-spec as a dependency or ensure it's available at spec/generated/python/"
            ) from e
        loader = importlib.machinery.SourceFileLoader("loza_generated_spec_contract_backup", str(backup_path))
        spec = importlib.util.spec_from_loader(loader.name, loader)
        if spec is None:
            raise ImportError(f"Cannot load fallback contract module from {backup_path}") from e
        backup = importlib.util.module_from_spec(spec)
        sys.modules[loader.name] = backup
        loader.exec_module(backup)

        LOZA_SPEC_VERSION = backup.LOZA_SPEC_VERSION
        LOZA_INGEST_API_VERSION = backup.LOZA_INGEST_API_VERSION
        LOZA_EVENT_VERSION = backup.LOZA_EVENT_VERSION
        MAX_EVENT_BYTES = getattr(backup, "MAX_EVENT_BYTES", 65536)
        ALLOWED_TOP_LEVEL_FIELDS = backup.ALLOWED_TOP_LEVEL_FIELDS
        CANONICAL_FIELDS = backup.CANONICAL_FIELDS
        ALLOWED_KINDS = backup.ALLOWED_KINDS
        ALLOWED_LEVELS = backup.ALLOWED_LEVELS
        ALLOWED_OUTCOMES = backup.ALLOWED_OUTCOMES
        ALLOWED_PARTIAL_REASONS = backup.ALLOWED_PARTIAL_REASONS
        ALLOWED_EVENT_STATES = backup.ALLOWED_EVENT_STATES
        CollectorAck = backup.CollectorAck
        CollectorError = backup.CollectorError
        CollectorResponse = backup.CollectorResponse
        parse_collector_response = backup.parse_collector_response
        looks_like_loza_event = backup.looks_like_loza_event
        normalize_event_aliases = backup.normalize_event_aliases
        build_ingest_envelope = backup.build_ingest_envelope
        validate_event_payload = backup.validate_event_payload
        validate_event_payload_detailed = getattr(
            backup, "validate_event_payload_detailed", backup.validate_event_payload
        )
        validate_flexible_json_bytes = getattr(
            backup, "validate_flexible_json_bytes", _validate_flexible_json_bytes_unavailable
        )
        ValidationError = getattr(backup, "ValidationError", ValueError)
        ValidationErrors = getattr(backup, "ValidationErrors", list)

__all__ = [
    "LOZA_SPEC_VERSION",
    "LOZA_INGEST_API_VERSION",
    "LOZA_EVENT_VERSION",
    "MAX_EVENT_BYTES",
    "ALLOWED_TOP_LEVEL_FIELDS",
    "CANONICAL_FIELDS",
    "ALLOWED_KINDS",
    "ALLOWED_LEVELS",
    "ALLOWED_OUTCOMES",
    "ALLOWED_PARTIAL_REASONS",
    "ALLOWED_EVENT_STATES",
    "CollectorAck",
    "CollectorError",
    "CollectorResponse",
    "parse_collector_response",
    "looks_like_loza_event",
    "normalize_event_aliases",
    "build_ingest_envelope",
    "validate_event_payload",
    "validate_event_payload_detailed",
    "validate_flexible_json_bytes",
    "ValidationError",
    "ValidationErrors",
]
