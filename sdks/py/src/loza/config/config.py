from __future__ import annotations

import os
from pathlib import Path
from dataclasses import dataclass, field, fields as dataclass_fields, replace
from typing import Any, Callable, Protocol, runtime_checkable

from ..sinks.stdout import StdoutSink

CanonicalWins = "canonical_wins"
UserWins = "user_wins"
FirstWins = "first_wins"
LastWins = "last_wins"
KeepBoth = "keep_both"
ErrorOnDuplicate = "error_on_duplicate"

ExpandDotKeys = "expand_dot_keys"
PreserveDotKeys = "preserve_dot_keys"
SnakeCaseKeys = "snake_case_keys"
CamelCaseKeys = "camel_case_keys"


class _Unset:
    """Sentinel for a config field the caller did not specify.

    It behaves like the field's default for ordinary reads (truthiness,
    equality, repr), so the public API is unchanged, but the config merge
    layer can tell "not specified" apart from an explicit ``False``/default
    value by identity. This lets higher-precedence layers explicitly disable
    flags enabled by lower layers.
    """

    __slots__ = ("_default",)

    def __init__(self, default: Any) -> None:
        self._default = default

    def __bool__(self) -> bool:
        return bool(self._default)

    def __eq__(self, other: object) -> bool:
        if other is self:
            return True
        if isinstance(other, _Unset):
            return self._default == other._default
        return self._default == other

    def __hash__(self) -> int:
        return hash(self._default)

    def __repr__(self) -> str:
        return repr(self._default)

    def __copy__(self) -> "_Unset":
        return self

    def __deepcopy__(self, memo: dict) -> "_Unset":
        return self


_UNSET_FALSE = _Unset(False)
_UNSET_CANONICAL = _Unset(CanonicalWins)


@runtime_checkable
class StatsHandler(Protocol):
    """Receives logger pipeline telemetry callbacks."""

    def on_emit(self, event: Any) -> None: ...
    def on_drop(self, reason: str) -> None: ...
    def on_error(self, error: Exception) -> None: ...
    def on_collector_ack(self, acks: list, errors: list, request_id: str, deduped: int) -> None: ...


@runtime_checkable
class DeliveryFailureHandler(StatsHandler, Protocol):
    """Optional extension for explicit delivery-failure callbacks."""

    def on_delivery_failed(self, event: Any, error: Exception) -> None: ...


@dataclass(slots=True)
class AsyncConfig:
    enabled: bool = _UNSET_FALSE  # type: ignore[assignment]
    queue_size: int = 8192
    workers: int = 1
    max_batch_bytes: int = 256 * 1024


@dataclass(slots=True)
class SecurityConfig:
    redact_by_default: bool = True
    allow_pii: bool = False
    max_field_bytes: int = 4096
    max_event_bytes: int = 256 * 1024
    max_attr_count: int = 512
    drop_oversized_events: bool = True


@dataclass(slots=True)
class FieldNamingConfig:
    policy: str = ExpandDotKeys
    custom_mapper: Callable[[str], str] | None = None


@dataclass(slots=True)
class Config:
    service: str = ""
    version: str = ""
    environment: str = "development"
    region: str = ""
    level: str = "info"
    strict: bool = _UNSET_FALSE  # type: ignore[assignment]
    sinks: list[Any] = field(default_factory=list)
    collector_endpoint: str = ""
    duplicate_policy: str = _UNSET_CANONICAL  # type: ignore[assignment]
    schema: Any = None
    sampler: Any = None
    redactor: Any = None
    metrics: Any = None
    stats_handler: Any = None
    async_config: AsyncConfig = field(default_factory=AsyncConfig)
    security: SecurityConfig = field(default_factory=SecurityConfig)
    field_naming: FieldNamingConfig = field(default_factory=FieldNamingConfig)
    checkpoint_emit_immediately: bool = _UNSET_FALSE  # type: ignore[assignment]
    deployment_id: str = ""
    include_host: bool = False
    max_checkpoints: int = 32
    panic_recovery: bool = False

    @classmethod
    def dev(cls, service: str = "") -> "Config":
        return cls(service=service, environment="development", level="debug", sinks=[StdoutSink()])

    @classmethod
    def production(cls, service: str = "") -> "Config":
        return cls(service=service, environment="production", level="info", strict=True, sinks=[StdoutSink()])

    @classmethod
    def test(cls, service: str = "") -> "Config":
        return cls(service=service, environment="test", level="debug", strict=True, sinks=[])

    def with_service(self, service: str) -> "Config":
        return replace(self, service=service)

    def with_version(self, version: str) -> "Config":
        return replace(self, version=version)

    def with_environment(self, environment: str) -> "Config":
        return replace(self, environment=environment)

    def with_region(self, region: str) -> "Config":
        return replace(self, region=region)

    def with_sink(self, sink: Any) -> "Config":
        return replace(self, sinks=[*self.sinks, sink])

    def with_sampler(self, sampler: Any) -> "Config":
        return replace(self, sampler=sampler)

    def with_redactor(self, redactor: Any) -> "Config":
        return replace(self, redactor=redactor)

    def with_metrics(self, metrics: Any) -> "Config":
        return replace(self, metrics=metrics)

    def with_schema(self, schema: Any) -> "Config":
        return replace(self, schema=schema)

    def with_event_schema(self, schema: Any) -> "Config":
        return self.with_schema(schema)

    def with_async(self, enabled: bool) -> "Config":
        return replace(self, async_config=replace(self.async_config, enabled=enabled))

    def with_collector_endpoint(self, endpoint: str) -> "Config":
        return replace(self, collector_endpoint=endpoint)

    def with_duplicate_policy(self, policy: str) -> "Config":
        return replace(self, duplicate_policy=policy)

    def validate(self) -> None:
        if self.level not in {"debug", "info", "notice", "warn", "error", "fatal"}:
            raise ValueError(f"unsupported level: {self.level}")
        if self.async_config.queue_size <= 0:
            raise ValueError("async queue_size must be positive")
        if self.async_config.workers <= 0:
            raise ValueError("async workers must be positive")
        if self.security.max_event_bytes <= 0:
            raise ValueError("max_event_bytes must be positive")
        if self.strict and not self.service:
            raise ValueError("strict mode requires service")


def load_layered_config() -> Config:
    raw: dict[str, Any] = {}
    defaults_path = _find_defaults_path()
    raw = _merge_dicts(raw, _parse_simple_yaml(defaults_path.read_text(encoding="utf-8")))
    user_path = _find_user_config_path()
    if user_path is not None and user_path.exists():
        raw = _merge_dicts(raw, _parse_simple_yaml(user_path.read_text(encoding="utf-8")))
    return _resolve_unset(_config_from_mapping(raw))


def _resolve_unset(cfg: Config) -> Config:
    """Replace any still-unset sentinels with their concrete default values."""
    if cfg.strict is _UNSET_FALSE:
        cfg.strict = False
    if cfg.duplicate_policy is _UNSET_CANONICAL:
        cfg.duplicate_policy = CanonicalWins
    if cfg.checkpoint_emit_immediately is _UNSET_FALSE:
        cfg.checkpoint_emit_immediately = False
    if cfg.async_config.enabled is _UNSET_FALSE:
        cfg.async_config.enabled = False
    return cfg


def new_client(code_config: Config):  # -> Logger
    """Create a Logger with 4-layer config precedence: defaults -> file -> env -> code."""
    # Step 1: Start with hardcoded defaults
    base = Config()

    # Step 2: Load defaults YAML + user YAML
    defaults_raw: dict[str, Any] = {}
    defaults_path = _find_defaults_path()
    if defaults_path.exists():
        defaults_raw = _parse_simple_yaml(defaults_path.read_text(encoding="utf-8"))
    user_path = _find_user_config_path()
    if user_path is not None and user_path.exists():
        defaults_raw = _merge_dicts(defaults_raw, _parse_simple_yaml(user_path.read_text(encoding="utf-8")))

    # Step 3: Apply file config to base (only non-empty values)
    file_cfg = _config_from_mapping(defaults_raw)
    merged = _merge_file_config(base, file_cfg)

    # Step 4: Apply env vars (override file config)
    merged = _apply_env_vars(merged)

    # Step 5: Apply code config (highest precedence)
    merged = _merge_code_config(merged, code_config)

    # Step 6: Collapse unset sentinels to concrete defaults, then validate
    merged = _resolve_unset(merged)
    merged.validate()

    from ..core.logger import Logger

    return Logger(merged)


def _apply_env_vars(cfg: Config) -> Config:
    """Apply environment variables to config, overriding file values."""
    env_map = {
        "LOZA_SERVICE": "service",
        "LOZA_SERVICE_NAME": "service",
        "LOZA_SERVICE_VERSION": "version",
        "LOZA_ENVIRONMENT": "environment",
        "LOZA_REGION": "region",
        "LOZA_LOG_LEVEL": "level",
        "LOZA_COLLECTOR_URL": "collector_endpoint",
        "LOZA_COLLECTOR_ENDPOINT": "collector_endpoint",
        "LOZA_DUPLICATE_POLICY": "duplicate_policy",
    }
    for env_key, cfg_field in env_map.items():
        val = os.getenv(env_key, "").strip()
        if val:
            setattr(cfg, cfg_field, val)

    # Strict mode from env
    strict_env = os.getenv("LOZA_STRICT", "").strip().lower()
    if strict_env in ("1", "true", "yes"):
        cfg.strict = True
    elif strict_env in ("0", "false", "no"):
        cfg.strict = False

    # Async from env
    async_env = os.getenv("LOZA_ASYNC_ENABLED", "").strip().lower()
    if async_env in ("1", "true", "yes"):
        cfg.async_config.enabled = True
    elif async_env in ("0", "false", "no"):
        cfg.async_config.enabled = False

    # Async queue size
    queue_env = os.getenv("LOZA_MAX_BUFFER_SIZE", "").strip()
    if queue_env.isdigit():
        cfg.async_config.queue_size = int(queue_env)

    # Batch size
    batch_env = os.getenv("LOZA_BATCH_SIZE", "").strip()
    if batch_env.isdigit():
        cfg.async_config.max_batch_bytes = int(batch_env)

    # Max event bytes
    max_bytes_env = os.getenv("LOZA_MAX_EVENT_BYTES", "").strip()
    if max_bytes_env.isdigit():
        cfg.security.max_event_bytes = int(max_bytes_env)

    return cfg


_NESTED_CONFIG_TYPES = {
    "async_config": AsyncConfig,
    "security": SecurityConfig,
    "field_naming": FieldNamingConfig,
}


def _overlay_missing_config_fields(base: Config, overlay: Config, names: tuple[str, ...]) -> None:
    """Copy non-default values for *names* from *overlay* onto *base*.

    The merge helpers used to rely on a hand-written whitelist that omitted the
    nested security/naming objects and several scalar settings, so values set in
    YAML or code were silently discarded. Nested objects are merged field by
    field so values from a lower-precedence layer are preserved unless the
    overlay explicitly changes them. Fields with dedicated handling (endpoints,
    credentials, sinks, plugins, ...) are intentionally not passed in *names*.
    """
    defaults = Config()
    for name in names:
        value = getattr(overlay, name)
        default_value = getattr(defaults, name)
        nested_type = _NESTED_CONFIG_TYPES.get(name)
        if nested_type is not None:
            base_nested = getattr(base, name)
            for nested_field in dataclass_fields(nested_type):
                field_value = getattr(value, nested_field.name)
                if field_value != getattr(default_value, nested_field.name):
                    setattr(base_nested, nested_field.name, field_value)
        elif value != default_value:
            setattr(base, name, value)


_MERGED_EXTRA_FIELDS = (
    "async_config",
    "security",
    "field_naming",
    "deployment_id",
    "include_host",
    "max_checkpoints",
    "panic_recovery",
)


def _merge_file_config(base: Config, file_cfg: Config) -> Config:
    """Apply file config values only when base has defaults (empty strings, default values)."""
    if file_cfg.service and not base.service:
        base.service = file_cfg.service
    if file_cfg.version and not base.version:
        base.version = file_cfg.version
    if file_cfg.environment and file_cfg.environment != "development":
        base.environment = file_cfg.environment
    if file_cfg.region and not base.region:
        base.region = file_cfg.region
    if file_cfg.level and file_cfg.level != "info":
        base.level = file_cfg.level
    if file_cfg.strict is not _UNSET_FALSE:
        base.strict = bool(file_cfg.strict)
    if file_cfg.collector_endpoint and not base.collector_endpoint:
        base.collector_endpoint = file_cfg.collector_endpoint
    if file_cfg.duplicate_policy is not _UNSET_CANONICAL:
        base.duplicate_policy = file_cfg.duplicate_policy
    if file_cfg.checkpoint_emit_immediately is not _UNSET_FALSE:
        base.checkpoint_emit_immediately = bool(file_cfg.checkpoint_emit_immediately)
    _overlay_missing_config_fields(base, file_cfg, _MERGED_EXTRA_FIELDS)
    return base


def _merge_code_config(base: Config, code: Config) -> Config:
    """Apply code config values (highest precedence). Non-default values override.

    Nested config objects are merged a field at a time so that values supplied by
    lower-precedence layers (file/env) are preserved for sub-fields the caller did
    not explicitly override.
    """
    if code.service:
        base.service = code.service
    if code.version:
        base.version = code.version
    if code.environment and code.environment != "development":
        base.environment = code.environment
    if code.region:
        base.region = code.region
    if code.level and code.level != "info":
        base.level = code.level
    if code.strict is not _UNSET_FALSE:
        base.strict = bool(code.strict)
    if code.collector_endpoint:
        base.collector_endpoint = code.collector_endpoint
    if code.duplicate_policy is not _UNSET_CANONICAL:
        base.duplicate_policy = code.duplicate_policy
    if code.sinks:
        base.sinks = code.sinks
    if code.sampler is not None:
        base.sampler = code.sampler
    if code.redactor is not None:
        base.redactor = code.redactor
    if code.metrics is not None:
        base.metrics = code.metrics
    if code.stats_handler is not None:
        base.stats_handler = code.stats_handler
    if code.schema is not None:
        base.schema = code.schema
    if code.checkpoint_emit_immediately is not _UNSET_FALSE:
        base.checkpoint_emit_immediately = bool(code.checkpoint_emit_immediately)
    if code.deployment_id:
        base.deployment_id = code.deployment_id
    if code.include_host:
        base.include_host = code.include_host
    if code.max_checkpoints != 32:
        base.max_checkpoints = code.max_checkpoints
    if code.panic_recovery:
        base.panic_recovery = code.panic_recovery

    if code.async_config.enabled is not _UNSET_FALSE:
        base.async_config.enabled = bool(code.async_config.enabled)
    if code.async_config.queue_size != 8192:
        base.async_config.queue_size = code.async_config.queue_size
    if code.async_config.workers != 1:
        base.async_config.workers = code.async_config.workers
    _overlay_missing_config_fields(base, code, _MERGED_EXTRA_FIELDS)

    if code.async_config.max_batch_bytes != 256 * 1024:
        base.async_config.max_batch_bytes = code.async_config.max_batch_bytes

    if not code.security.redact_by_default:
        base.security.redact_by_default = code.security.redact_by_default
    if code.security.allow_pii:
        base.security.allow_pii = code.security.allow_pii
    if code.security.max_field_bytes != 4096:
        base.security.max_field_bytes = code.security.max_field_bytes
    if code.security.max_event_bytes != 256 * 1024:
        base.security.max_event_bytes = code.security.max_event_bytes
    if code.security.max_attr_count != 512:
        base.security.max_attr_count = code.security.max_attr_count
    if not code.security.drop_oversized_events:
        base.security.drop_oversized_events = code.security.drop_oversized_events

    if code.field_naming.policy != ExpandDotKeys:
        base.field_naming.policy = code.field_naming.policy
    if code.field_naming.custom_mapper is not None:
        base.field_naming.custom_mapper = code.field_naming.custom_mapper
    return base


def _find_defaults_path() -> Path:
    override = os.getenv("LOZA_PY_DEFAULTS", "").strip()
    if override:
        return Path(override)
    here = Path(__file__).resolve()
    candidates = [
        here.parents[1] / "loza-py.defaults.yaml",
        here.parents[2] / "loza-py.defaults.yaml",
        Path.cwd() / "loza-py.defaults.yaml",
    ]
    for candidate in candidates:
        if candidate.exists():
            return candidate
    return candidates[0]


def _find_user_config_path() -> Path | None:
    override = os.getenv("LOZA_PY_CONFIG", "").strip()
    if override:
        return Path(override)
    for candidate in (Path.cwd() / ".loza-py.yaml", Path.cwd() / "loza.yaml"):
        if candidate.exists():
            return candidate
    return None


def _config_from_mapping(data: dict[str, Any]) -> Config:
    async_map = data.get("async_config") if isinstance(data.get("async_config"), dict) else {}
    security_map = data.get("security") if isinstance(data.get("security"), dict) else {}
    field_map = data.get("field_naming") if isinstance(data.get("field_naming"), dict) else {}
    return Config(
        service=str(data.get("service", "")),
        version=str(data.get("version", "")),
        environment=str(data.get("environment", "development")),
        region=str(data.get("region", "")),
        level=str(data.get("level", "info")),
        strict=_UNSET_FALSE if "strict" not in data else bool(data["strict"]),
        collector_endpoint=str(data.get("collector_endpoint", "")),
        duplicate_policy=_UNSET_CANONICAL if "duplicate_policy" not in data else str(data["duplicate_policy"]),
        checkpoint_emit_immediately=(
            _UNSET_FALSE if "checkpoint_emit_immediately" not in data else bool(data["checkpoint_emit_immediately"])
        ),
        deployment_id=str(data.get("deployment_id", "")),
        include_host=bool(data.get("include_host", False)),
        max_checkpoints=int(data.get("max_checkpoints", 32)),
        panic_recovery=bool(data.get("panic_recovery", False)),
        async_config=AsyncConfig(
            enabled=_UNSET_FALSE if "enabled" not in async_map else bool(async_map["enabled"]),
            queue_size=int(async_map.get("queue_size", 8192)),
            workers=int(async_map.get("workers", 1)),
            max_batch_bytes=int(async_map.get("max_batch_bytes", 256 * 1024)),
        ),
        security=SecurityConfig(
            redact_by_default=bool(security_map.get("redact_by_default", True)),
            allow_pii=bool(security_map.get("allow_pii", False)),
            max_field_bytes=int(security_map.get("max_field_bytes", 4096)),
            max_event_bytes=int(security_map.get("max_event_bytes", 256 * 1024)),
            max_attr_count=int(security_map.get("max_attr_count", 512)),
            drop_oversized_events=bool(security_map.get("drop_oversized_events", True)),
        ),
        field_naming=FieldNamingConfig(
            policy=str(field_map.get("policy", ExpandDotKeys)),
            custom_mapper=None,
        ),
    )


def _merge_dicts(base: dict[str, Any], override: dict[str, Any]) -> dict[str, Any]:
    merged = dict(base)
    for k, v in override.items():
        if isinstance(v, dict) and isinstance(merged.get(k), dict):
            merged[k] = _merge_dicts(merged[k], v)
        else:
            merged[k] = v
    return merged


def _strip_inline_comment(line: str) -> str:
    """Strip a trailing inline comment without corrupting quoted or token values.

    A ``#`` opens a comment only when it is outside quotes and starts a token
    (at the start of the line or preceded by whitespace). This preserves ``#``
    inside quoted values such as URL fragments and inside unquoted tokens such
    as ``ab#cd``.
    """
    quote: str | None = None
    index = 0
    length = len(line)
    while index < length:
        char = line[index]
        if quote is not None:
            if quote == '"' and char == "\\" and index + 1 < length:
                index += 2  # skip a backslash-escaped character
                continue
            if char == quote:
                # YAML escapes a single quote inside single quotes by doubling it.
                if char == "'" and index + 1 < length and line[index + 1] == "'":
                    index += 2
                    continue
                quote = None
        elif char in ("'", '"'):
            quote = char
        elif char == "#" and (index == 0 or line[index - 1].isspace()):
            return line[:index]
        index += 1
    return line


def _parse_simple_yaml(content: str) -> dict[str, Any]:
    root: dict[str, Any] = {}
    stack: list[tuple[int, dict[str, Any]]] = [(-1, root)]
    for raw_line in content.splitlines():
        if not raw_line.strip() or raw_line.lstrip().startswith("#"):
            continue
        indent = len(raw_line) - len(raw_line.lstrip(" "))
        line = _strip_inline_comment(raw_line).rstrip()
        if ":" not in line:
            continue
        key, raw_value = line.lstrip().split(":", 1)
        key = key.strip()
        value = raw_value.strip()
        while len(stack) > 1 and indent <= stack[-1][0]:
            stack.pop()
        current = stack[-1][1]
        if value == "":
            child: dict[str, Any] = {}
            current[key] = child
            stack.append((indent, child))
            continue
        current[key] = _parse_scalar(value)
    return root


def _parse_scalar(value: str) -> Any:
    if len(value) >= 2 and value[0] == value[-1] and value[0] in {"'", '"'}:
        value = value[1:-1]
    lower = value.lower()
    if lower in {"true", "false"}:
        return lower == "true"
    if value.isdigit():
        return int(value)
    return value
