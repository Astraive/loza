from __future__ import annotations

import copy
import json
import sys
import time
from contextvars import ContextVar
from dataclasses import replace
from datetime import datetime, timezone
from typing import Any, Callable

from .config import (
    CanonicalWins,
    Config,
    ErrorOnDuplicate,
    FirstWins,
    KeepBoth,
    LastWins,
    UserWins,
    _collector_ingest_endpoint,
)
from .duplicate_policy import apply_duplicate
from .errors import EventClosedError, EventValidationError
from .hashing import hash_sensitive_value
from .event import (
    EVENT_ACTIVE,
    EVENT_DELIVERY_FAILED,
    EVENT_EMITTED,
    EVENT_EMITTING,
    EVENT_FINISHED,
    EVENT_FAILED_VALIDATION,
    Attr,
    EventContext,
    Params,
    sanitize_event,
)
from .pipeline import MemoryOfflineBuffer, Pipeline
from .redactor import default_redactor
from .schema import DefaultSchema, EventView
from ..sinks.httpbatch import HTTPBatchSink
from ..generated.spec_contract import validate_event_payload


# Ambient "current event" shared across all Logger instances. A ContextVar keeps the
# binding local to the current thread/task so concurrent requests do not see each
# other's events.
_current_event: ContextVar[EventContext | None] = ContextVar("loza_current_event", default=None)


class Logger:
    def __init__(self, config: Config) -> None:
        config.validate()
        self._config = config
        self._pipeline: Pipeline | None = None
        self._metrics = config.metrics
        self._stats_handler = config.stats_handler
        self._install_default_collector_sink()
        if not self._config.sinks and self._config.environment != "test":
            self._config = replace(self._config, sinks=Config.dev(config.service).sinks)
        if self._config.sampler is None:
            self._config.sampler = lambda event: True
        if self._config.redactor is None:
            self._config.redactor = default_redactor()
        if self._config.schema is None:
            self._config.schema = DefaultSchema()
        if self._config.async_config.enabled and self._config.sinks:
            self._pipeline = Pipeline(
                self._config.sinks,
                queue_size=self._config.async_config.queue_size,
                max_batch_bytes=self._config.async_config.max_batch_bytes,
                offline_buffer=MemoryOfflineBuffer(self._config.async_config.queue_size),
                metrics=self._metrics,
            )
            self._pipeline.start(self._config.async_config.workers)

    def _install_default_collector_sink(self) -> None:
        endpoint = self._config.collector_endpoint.strip()
        if not endpoint:
            return
        if any(isinstance(sink, HTTPBatchSink) for sink in self._config.sinks):
            return
        if not self._config.sinks or all(self._is_default_terminal_sink(sink) for sink in self._config.sinks):
            self._config = replace(
                self._config,
                sinks=[
                    HTTPBatchSink(
                        _collector_ingest_endpoint(endpoint, self._config.collector_name),
                        api_key=self._config.api_key,
                        username=self._config.username,
                        password=self._config.password,
                        service=self._config.service,
                    )
                ],
            )

    @staticmethod
    def _is_default_terminal_sink(sink: object) -> bool:
        return sink.__class__.__name__ in {"StdoutSink", "StderrSink"}

    def alias(self, name: str) -> "Logger":
        """Create an immutable child logger that preserves config and emits loza.alias."""
        cfg = replace(self._config, alias=name)
        return Logger(cfg)

    def _reconfigure(self, config: Config) -> None:
        """Reconfigure this logger in-place (used by configure() to update the exported loza instance)."""
        config.validate()
        # Shut down existing pipeline if any
        if self._pipeline is not None:
            self._pipeline.stop()
            self._pipeline = None
        self._config = config
        self._metrics = config.metrics
        self._stats_handler = config.stats_handler
        self._install_default_collector_sink()
        if not self._config.sinks and self._config.environment != "test":
            self._config = replace(self._config, sinks=Config.dev(config.service).sinks)
        if self._config.sampler is None:
            self._config.sampler = lambda event: True
        if self._config.redactor is None:
            self._config.redactor = default_redactor()
        if self._config.schema is None:
            self._config.schema = DefaultSchema()
        if self._config.async_config.enabled and self._config.sinks:
            self._pipeline = Pipeline(
                self._config.sinks,
                queue_size=self._config.async_config.queue_size,
                max_batch_bytes=self._config.async_config.max_batch_bytes,
                offline_buffer=MemoryOfflineBuffer(self._config.async_config.queue_size),
                metrics=self._metrics,
            )
            self._pipeline.start(self._config.async_config.workers)

    def start_event(self, params: Params) -> EventContext:
        params = replace(params, custom=list(params.custom))
        if not params.service and self._config.service:
            params.service = self._config.service
        if not params.version and self._config.version:
            params.version = self._config.version
        if not params.environment and self._config.environment:
            params.environment = self._config.environment
        if self._config.alias:
            params.custom.append(Attr("loza.alias", self._config.alias))
        if not params.region and self._config.region:
            params.region = self._config.region
        if not params.deployment_id and self._config.deployment_id:
            params.deployment_id = self._config.deployment_id
        if self._config.include_host and not params.host:
            import socket

            params.host = socket.gethostname()
        ctx = EventContext(service=params.service or self._config.service, params=params)
        ctx._max_checkpoints = self._config.max_checkpoints
        for attr in params.custom:
            self._apply_attr(ctx, attr)
        if params.user_id:
            self.merge(ctx, "user", id=params.user_id)
        if params.tenant_id:
            self.merge(ctx, "tenant", id=params.tenant_id)
        if params.workspace_id:
            self.merge(ctx, "tenant", workspace_id=params.workspace_id)
        if self._config.checkpoint_emit_immediately:
            ctx._checkpoint_writer = self._emit_checkpoint_immediate
        if self._metrics is not None:
            self._metrics.on_event_created()
        return ctx

    def enrich(self, ctx: EventContext, *attrs: Attr, **named: Any) -> None:
        ctx._ensure_mutable()
        ctx._mark_active()
        for attr in attrs:
            self._apply_attr(ctx, attr)
        for key, value in named.items():
            self._apply_attr(ctx, Attr(key, value))

    def append(self, ctx: EventContext, *attrs: Attr, **named: Any) -> None:
        self.enrich(ctx, *attrs, **named)

    @staticmethod
    def _register_policy_keys(ctx: EventContext, attr: Attr) -> bool:
        """Register drop/hash/sensitive flags for a key. Returns True when the attr is dropped."""
        if attr.drop:
            ctx._drop_keys.add(attr.key)
            return True
        if attr.hash_value:
            ctx._hash_keys.add(attr.key)
        if attr.sensitive:
            ctx._sensitive_keys.add(attr.key)
        return False

    def _apply_attr(self, ctx: EventContext, attr: Attr) -> None:
        if self._register_policy_keys(ctx, attr):
            return
        if attr.key.startswith("user."):
            self._set_path(ctx.user, attr.key.removeprefix("user."), attr.value, self._config.duplicate_policy)
            return
        if attr.key.startswith("tenant."):
            self._set_path(ctx.tenant, attr.key.removeprefix("tenant."), attr.value, self._config.duplicate_policy)
            return
        if attr.key.startswith("resource."):
            self._set_path(ctx.resource, attr.key.removeprefix("resource."), attr.value, self._config.duplicate_policy)
            return
        if attr.key.startswith("http."):
            self._set_path(ctx.http, attr.key.removeprefix("http."), attr.value, self._config.duplicate_policy)
            return
        self._set_path(ctx.attrs, attr.key, attr.value, self._config.duplicate_policy)

    def checkpoint(self, ctx: EventContext, name: str, *attrs: Attr, **named: Any) -> None:
        data = {a.key: a.value for a in attrs}
        for attr in attrs:
            self._register_policy_keys(ctx, attr)
        data.update(named)
        ctx.checkpoint(name, **data)

    def set(self, ctx: EventContext, *attrs: Attr | str, **named: Any) -> None:
        if attrs and all(isinstance(item, str) for item in attrs):
            if len(attrs) % 2:
                raise TypeError("set string attributes must be key/value pairs")
            named = {**dict(zip(attrs[::2], attrs[1::2])), **named}
            attrs = ()
        self.enrich(ctx, *attrs, **named)

    def merge(self, ctx: EventContext, group: str, *attrs: Attr, **named: Any) -> None:
        ctx._ensure_mutable()
        ctx._mark_active()
        target = self._target_map(ctx, group)
        for attr in attrs:
            self._set_path(target, attr.key, attr.value, self._config.duplicate_policy)
        for key, value in named.items():
            self._set_path(target, key, value, self._config.duplicate_policy)

    def _legacy_enrich(self, ctx: EventContext, **attrs: Any) -> None:
        for key, value in attrs.items():
            self._set_path(ctx.attrs, key, value)

    def delete(self, ctx: EventContext, *keys: str) -> None:
        ctx._ensure_mutable()
        ctx._mark_active()
        for key in keys:
            self._delete_path(ctx.attrs, key)

    def get(self, ctx: EventContext, key: str) -> Any:
        bucket, local_key = self._resolve_bucket(ctx, key)
        return self._get_path(bucket, local_key)

    def get_group(self, ctx: EventContext, name: str) -> dict[str, Any] | None:
        groups = {
            "user": ctx.user,
            "tenant": ctx.tenant,
            "resource": ctx.resource,
            "http": ctx.http,
            "attrs": ctx.attrs,
        }
        value = groups.get(name)
        if value is not None:
            return value
        nested = ctx.attrs.get(name)
        return nested if isinstance(nested, dict) else None

    def finish(self, ctx: EventContext, outcome: str, *attrs: Attr, **named: Any) -> None:
        if attrs or named:
            self.enrich(ctx, *attrs, **named)
        ctx.finish(outcome)
        if self._metrics is not None:
            self._metrics.on_event_finished()

    def finish_error(self, ctx: EventContext, error: Exception, *attrs: Attr, **named: Any) -> None:
        if attrs or named:
            self.enrich(ctx, *attrs, **named)
        ctx.finish_error(error)
        if self._metrics is not None:
            self._metrics.on_event_finished()

    def emit(self, ctx: EventContext) -> str:
        if self._config.panic_recovery:
            try:
                return self._emit_inner(ctx)
            except BaseException:
                ctx.event_state = EVENT_DELIVERY_FAILED
                ctx.emitted = False
                raise
        return self._emit_inner(ctx)

    def _emit_inner(self, ctx: EventContext) -> str:
        if ctx.event_state == EVENT_EMITTED or ctx.emitted:
            return ctx.emitted_payload
        if ctx.event_state in {EVENT_EMITTING, EVENT_DELIVERY_FAILED, EVENT_FAILED_VALIDATION}:
            raise EventClosedError(f"event {ctx.event_id} is closed in state {ctx.event_state}")

        emit_start = time.monotonic()
        try:
            self._validate(ctx)

            if not self._config.sampler(ctx):
                ctx.event_state = EVENT_EMITTED
                ctx.emitted = True
                ctx.emitted_payload = ""
                self._notify_drop("sampled_out")
                return ""

            ctx.event_state = EVENT_EMITTING
            ctx.delivery_attempts += 1
            self._apply_duplicate_field_policy(ctx)
            self._enforce_attr_limit(ctx)
            safe_ctx = self._sanitize_event(ctx)
            view = EventView(safe_ctx)
            payload = self._config.schema.encode(view)
            payload = self._config.redactor(payload)
            encoded = json.dumps(payload, separators=(",", ":"), sort_keys=True)
            if len(encoded.encode("utf-8")) > self._config.security.max_event_bytes:
                if self._config.security.drop_oversized_events:
                    ctx.event_state = EVENT_EMITTED
                    ctx.emitted = True
                    ctx.emitted_payload = ""
                    self._notify_drop("oversized_event")
                    return ""
                raise EventValidationError("event exceeds max_event_bytes")
            if self._pipeline is not None:
                accepted = self._pipeline.try_enqueue(encoded)
                if not accepted and self._pipeline.offline_buffer is None:
                    self._notify_drop("queue_full")
                    raise EventValidationError("async pipeline backpressure drop")
            else:
                delivery_failed = False
                for sink in self._config.sinks:
                    try:
                        sink.write(encoded)
                    except Exception as sink_err:
                        delivery_failed = True
                        self._notify_delivery_failed(ctx, sink_err)
                if delivery_failed:
                    ctx.event_state = EVENT_DELIVERY_FAILED
                    ctx.emitted = False
                    ctx.emitted_payload = ""
                    return ""
            ctx.emitted = True
            ctx.event_state = EVENT_EMITTED
            ctx.emitted_payload = encoded
            self._notify_emit(ctx)
            return encoded
        except EventValidationError:
            ctx.event_state = EVENT_FAILED_VALIDATION
            ctx.emitted = False
            self._notify_emit_failure(ctx)
            raise
        except ValueError as exc:
            ctx.event_state = EVENT_FAILED_VALIDATION
            ctx.emitted = False
            self._notify_emit_failure(ctx)
            raise EventValidationError(str(exc)) from exc
        except Exception as exc:
            ctx.event_state = EVENT_DELIVERY_FAILED
            ctx.emitted = False
            self._notify_delivery_failed(ctx, exc)
            raise
        finally:
            if self._metrics is not None:
                self._metrics.observe_emit_duration(time.monotonic() - emit_start)

    def flush(self, timeout: float = 30.0) -> None:
        if self._pipeline is not None:
            self._pipeline.drain_once()
            self._pipeline.replay_offline()
        for sink in self._config.sinks:
            flush = getattr(sink, "flush", None)
            if callable(flush):
                flush()

    def shutdown(self, timeout: float = 30.0) -> None:
        if self._pipeline is not None:
            self._pipeline.close(timeout=timeout)
            self._pipeline = None
        else:
            self.flush(timeout=timeout)
        for sink in self._config.sinks:
            close = getattr(sink, "close", None)
            if callable(close):
                close()

    def drain(self, timeout: float = 30.0) -> list:
        """Drain the pipeline — stop accepting new events and flush pending."""
        if self._pipeline is not None:
            return self._pipeline.drain()
        self.flush(timeout=timeout)
        return []

    def pause(self) -> None:
        """Pause event emission."""
        for sink in self._config.sinks:
            pause_fn = getattr(sink, "pause", None)
            if callable(pause_fn):
                pause_fn()

    def resume(self) -> None:
        """Resume event emission."""
        for sink in self._config.sinks:
            resume_fn = getattr(sink, "resume", None)
            if callable(resume_fn):
                resume_fn()

    def queue_size(self) -> int:
        """Return the current sink queue size."""
        for sink in self._config.sinks:
            qs = getattr(sink, "queue_size", None)
            if callable(qs):
                return qs()
        if self._pipeline is not None:
            return getattr(self._pipeline, "size", lambda: 0)()
        return 0

    def health(self) -> bool:
        """Return SDK health status."""
        for sink in self._config.sinks:
            h = getattr(sink, "health", None)
            if callable(h):
                return h()
        return True

    def debug(self, message: str, **attrs: Any) -> str:
        return self._emit_immediate("debug", message, **attrs)

    def info(self, message: str, **attrs: Any) -> str:
        return self._emit_immediate("info", message, **attrs)

    def notice(self, message: str, **attrs: Any) -> str:
        return self._emit_immediate("notice", message, **attrs)

    def warn(self, message: str, **attrs: Any) -> str:
        return self._emit_immediate("warn", message, **attrs)

    def error(self, message: str, **attrs: Any) -> str:
        return self._emit_immediate("error", message, **attrs)

    def fatal(self, message: str, **attrs: Any) -> str:
        result = self._emit_immediate("fatal", message, **attrs)
        if self._config.exit_on_fatal:
            self.flush()
            sys.exit(1)
        return result

    def event(self, name: str, **attrs: Any) -> str:
        ctx = self.start_event(Params(event=name, kind="event", level="info", message=name))
        self.enrich(ctx, **attrs)
        self.finish(ctx, "success")
        return self.emit(ctx)

    def track(self, name: str, **attrs: Any) -> str:
        ctx = self.start_event(Params(event=name, kind="event", level="info", message=name))
        self.enrich(ctx, **attrs)
        self.finish(ctx, "success")
        return self.emit(ctx)

    def audit(self, name: str, **attrs: Any) -> str:
        ctx = self.start_event(Params(event=name, kind="event", level="info", message=name))
        self.enrich(ctx, **attrs)
        self.finish(ctx, "success")
        return self.emit(ctx)

    def security(self, name: str, **attrs: Any) -> str:
        ctx = self.start_event(Params(event=name, kind="event", level="warn", message=name))
        self.enrich(ctx, **attrs)
        self.finish(ctx, "success")
        return self.emit(ctx)

    def metric(self, name: str, value: Any, **attrs: Any) -> str:
        ctx = self.start_event(Params(event=name, kind="event", level="info", message=name))
        self.enrich(ctx, metric_value=value, **attrs)
        self.finish(ctx, "success")
        return self.emit(ctx)

    def count(self, name: str, value: int = 1, **attrs: Any) -> str:
        ctx = self.start_event(Params(event=name, kind="event", level="info", message=name))
        self.enrich(ctx, metric_kind="count", count=value, **attrs)
        self.finish(ctx, "success")
        return self.emit(ctx)

    def gauge(self, name: str, value: float, **attrs: Any) -> str:
        ctx = self.start_event(Params(event=name, kind="event", level="info", message=name))
        self.enrich(ctx, metric_kind="gauge", gauge=value, **attrs)
        self.finish(ctx, "success")
        return self.emit(ctx)

    def histogram(self, name: str, value: float, **attrs: Any) -> str:
        ctx = self.start_event(Params(event=name, kind="event", level="info", message=name))
        self.enrich(ctx, metric_kind="histogram", histogram_value=value, **attrs)
        self.finish(ctx, "success")
        return self.emit(ctx)

    def breadcrumb(self, name: str, **attrs: Any) -> str:
        ctx = self.start_event(Params(event=name, kind="checkpoint", level="debug", message=name))
        self.enrich(ctx, **attrs)
        self.finish(ctx, "success")
        return self.emit(ctx)

    def _emit_immediate(self, level: str, message: str, **attrs: Any) -> str:
        ctx = self.start_event(Params(event=f"log.{level}", kind="log", message=message, level=level))
        self.enrich(ctx, **attrs)
        self.finish(ctx, "success")
        return self.emit(ctx)

    def drop(self, ctx: EventContext, reason: str = "dropped", *attrs: Attr, **named: Any) -> None:
        if attrs or named:
            self.enrich(ctx, *attrs, **named)
        ctx.finish("abandoned")
        ctx.event_state = EVENT_EMITTED
        ctx.emitted = True
        ctx.emitted_payload = ""

    def cancel(self, ctx: EventContext, reason: str = "cancelled", *attrs: Attr, **named: Any) -> None:
        if attrs or named:
            self.enrich(ctx, *attrs, **named)
        ctx.finish("cancelled")
        ctx.event_state = EVENT_EMITTED
        ctx.emitted = True
        ctx.emitted_payload = ""

    def abandon(self, ctx: EventContext, reason: str = "abandoned", *attrs: Attr, **named: Any) -> None:
        if attrs or named:
            self.enrich(ctx, *attrs, **named)
        ctx.finish("abandoned")

    def retry(self, ctx: EventContext, *attrs: Attr, **named: Any) -> None:
        """Reopen a closed emission so the event can be delivered again.

        A sink failure leaves an event in ``EVENT_DELIVERY_FAILED`` with
        ``emitted=False``. Such an event is otherwise permanently closed:
        ``enrich``/``finish`` reject it via ``_ensure_mutable`` and ``emit``
        raises ``EventClosedError``, so the payload can never be re-sent.
        Reopen that state here so a subsequent ``emit()`` re-delivers it, and
        record the ``retried`` outcome without failing on an already finished
        event.
        """
        if ctx.event_state in {EVENT_DELIVERY_FAILED, EVENT_EMITTED}:
            # Reopen through a mutable state so retry attrs can still be applied
            # now that finished events are closed to mutation. The state is
            # restored to EVENT_FINISHED below before the event is re-emitted.
            ctx.event_state = EVENT_ACTIVE
            ctx.emitted = False
            ctx.emitted_payload = ""
        if attrs or named:
            self.enrich(ctx, *attrs, **named)
        if ctx.finished_at is None:
            ctx.finish("retried")
        else:
            # A previously finished event cannot be finished twice; the reopen
            # above still makes it emittable, so just update the outcome and
            # restore the closed state.
            ctx.outcome = "retried"
            ctx.event_state = EVENT_FINISHED

    def partial(self, ctx: EventContext, reason: str = "partial", *attrs: Attr, **named: Any) -> None:
        if attrs or named:
            self.enrich(ctx, *attrs, **named)
        ctx.mark_partial(reason)

    def clone_event(self, ctx: EventContext) -> EventContext:
        import copy

        return copy.deepcopy(ctx)

    def link_event(self, parent: EventContext, child: EventContext) -> None:
        if not parent.params.trace_id and child.params.trace_id:
            parent.params.trace_id = child.params.trace_id
        if not parent.params.span_id and child.params.span_id:
            parent.params.span_id = child.params.span_id
        parent.params.links = list(parent.params.links) + [child.event_id]

    def current_event(self) -> EventContext | None:
        """Return the event bound to the current context, if any."""
        return _current_event.get()

    def bind_event(self, ctx: EventContext) -> "Logger":
        """Bind ``ctx`` as the current event for this context and return self."""
        _current_event.set(ctx)
        return self

    def unbind_event(self) -> EventContext | None:
        """Clear the current event for this context, returning the previously bound event."""
        previous = _current_event.get()
        _current_event.set(None)
        return previous

    def wrap(self, ctx: EventContext, fn: Callable[..., Any], *args: Any, **kwargs: Any) -> Any:
        try:
            result = fn(*args, **kwargs)
            self.finish(ctx, "success")
            return result
        except Exception as exc:
            self.finish_error(ctx, exc)
            raise

    def _emit_checkpoint_immediate(self, ctx: EventContext, checkpoint: dict[str, object]) -> None:
        try:
            now = datetime.now(timezone.utc)
            params = Params(
                event=f"checkpoint.{checkpoint['name']}",
                kind="checkpoint",
                message=str(checkpoint["name"]),
                level=ctx.params.level,
                service=ctx.params.service,
                version=ctx.params.version,
                environment=ctx.params.environment,
                region=ctx.params.region,
                request_id=ctx.params.request_id or ctx.request_id,
                trace_id=ctx.params.trace_id or ctx.trace_id,
                span_id=ctx.params.span_id or ctx.span_id,
            )
            snapshot = EventContext(service=ctx.service, params=params)
            snapshot.started_at = ctx.started_at
            snapshot.finished_at = now
            snapshot.event_state = EVENT_FINISHED
            snapshot._drop_keys = set(ctx._drop_keys)
            snapshot._hash_keys = set(ctx._hash_keys)
            snapshot._sensitive_keys = set(ctx._sensitive_keys)
            snapshot.attrs = {key: value for key, value in checkpoint.items() if key not in {"name", "at_ms"}}
            safe_snapshot = self._sanitize_event(snapshot)
            payload = self._config.schema.encode(EventView(safe_snapshot))
            if isinstance(payload, dict):
                payload.setdefault("duration_ms", checkpoint.get("at_ms", 0))
            payload = self._config.redactor(payload)
            encoded = json.dumps(payload, separators=(",", ":"), sort_keys=True)
            if self._pipeline is not None:
                self._pipeline.write_sync(encoded)
            else:
                for sink in self._config.sinks:
                    sink.write(encoded)
        except Exception as exc:
            print(f"[loza] checkpoint emit error: {exc}", file=sys.stderr)

    def _validate(self, ctx: EventContext) -> None:
        if not self._config.strict:
            return
        payload = ctx.to_dict()
        try:
            validate_event_payload(payload, strict=True)
        except ValueError as exc:
            raise EventValidationError(str(exc)) from exc

    def _apply_duplicate_field_policy(self, ctx: EventContext) -> None:
        from .canonical import is_canonical

        policy = self._config.duplicate_policy
        keys_to_remove: list[str] = []
        for key in list(ctx.attrs.keys()):
            if not is_canonical(key):
                continue
            if policy in {CanonicalWins, FirstWins}:
                keys_to_remove.append(key)
            elif policy in {UserWins, LastWins}:
                pass  # user attr overwrites canonical — already in attrs
            elif policy == KeepBoth:
                ctx.attrs[f"attrs.{key}"] = ctx.attrs.pop(key)
            elif policy == ErrorOnDuplicate:
                raise EventValidationError(f"duplicate canonical field: {key}")
        for key in keys_to_remove:
            del ctx.attrs[key]

    def _notify_emit(self, ctx: EventContext) -> None:
        if self._metrics is not None:
            self._metrics.on_event_emitted(True)
        if self._stats_handler is not None:
            try:
                self._stats_handler.on_emit(ctx)
            except Exception:
                pass

    def _notify_emit_failure(self, ctx: EventContext) -> None:
        if self._metrics is not None:
            self._metrics.on_event_emitted(False)

    def _notify_drop(self, reason: str) -> None:
        if self._metrics is not None:
            self._metrics.on_event_dropped(reason)
        if self._stats_handler is not None:
            try:
                self._stats_handler.on_drop(reason)
            except Exception:
                pass

    def _notify_delivery_failed(self, ctx: EventContext, error: Exception) -> None:
        if self._metrics is not None:
            self._metrics.on_event_emitted(False)
        if self._stats_handler is not None:
            try:
                self._stats_handler.on_error(error)
            except Exception:
                pass
            from .config import DeliveryFailureHandler as _DFH

            if isinstance(self._stats_handler, _DFH):
                try:
                    self._stats_handler.on_delivery_failed(ctx, error)
                except Exception:
                    pass

    def _target_map(self, ctx: EventContext, group: str) -> dict[str, Any]:
        if group == "user":
            return ctx.user
        if group == "tenant":
            return ctx.tenant
        if group == "resource":
            return ctx.resource
        if group == "http":
            return ctx.http
        if group == "attrs":
            return ctx.attrs
        child = ctx.attrs.get(group)
        if not isinstance(child, dict):
            child = {}
            ctx.attrs[group] = child
        return child

    def _enforce_attr_limit(self, ctx: EventContext) -> None:
        max_attr_count = self._config.security.max_attr_count
        if max_attr_count <= 0:
            return

        buckets: list[dict[str, Any]] = [ctx.attrs, ctx.user, ctx.tenant, ctx.resource, ctx.http]
        total = sum(Logger._attr_leaf_count(value) for bucket in buckets for value in bucket.values())
        if total <= max_attr_count:
            return

        remaining = [max_attr_count]
        truncated = False
        for bucket in buckets:
            if Logger._keep_attr_leaves(bucket, remaining):
                truncated = True

        if truncated and "_truncated" not in ctx.attrs:
            ctx.attrs["_truncated"] = True

    @staticmethod
    def _attr_leaf_count(value: Any) -> int:
        """Count attribute leaves, descending into nested (dotted-key) dicts."""
        if isinstance(value, dict) and value:
            return sum(Logger._attr_leaf_count(child) for child in value.values())
        return 1

    @staticmethod
    def _keep_attr_leaves(target: dict[str, Any], budget: list[int]) -> bool:
        """Keep at most ``budget[0]`` leaves in ``target`` in insertion order.

        Nested dicts are descended into so dotted keys are accounted for, and
        parent dicts left empty by truncation are removed. ``budget`` is a
        one-element list so the shared cross-bucket allowance can be decremented
        in place. Returns True when any leaf was removed.
        """
        removed = False
        for key in list(target.keys()):
            if budget[0] <= 0:
                del target[key]
                removed = True
                continue
            value = target[key]
            if isinstance(value, dict) and value:
                if Logger._keep_attr_leaves(value, budget):
                    removed = True
                if not value:
                    del target[key]
                    removed = True
            else:
                budget[0] -= 1
        return removed

    @staticmethod
    def _resolve_bucket(ctx: EventContext, key: str) -> tuple[dict[str, Any], str]:
        """Return (bucket, local_key) for a potentially group-prefixed key."""
        for prefix, bucket in [
            ("user.", ctx.user),
            ("tenant.", ctx.tenant),
            ("resource.", ctx.resource),
            ("http.", ctx.http),
        ]:
            if key.startswith(prefix):
                return bucket, key[len(prefix) :]
        return ctx.attrs, key

    def _sanitize_event(self, ctx: EventContext) -> EventContext:
        """Create a sanitized copy: drop, hash, and redact sensitive attrs."""
        if not ctx._drop_keys and not ctx._hash_keys and not ctx._sensitive_keys:
            return ctx
        safe = copy.deepcopy(ctx)
        for key in ctx._drop_keys:
            bucket, local = self._resolve_bucket(safe, key)
            self._delete_path(bucket, local)
        for key in ctx._hash_keys:
            bucket, local = self._resolve_bucket(safe, key)
            self._hash_at_path(bucket, local)
        for key in ctx._sensitive_keys:
            bucket, local = self._resolve_bucket(safe, key)
            self._redact_at_path(bucket, local)
        for checkpoint in safe.checkpoints:
            self._sanitize_checkpoint(checkpoint, ctx._drop_keys, ctx._hash_keys, ctx._sensitive_keys)
        return safe

    @staticmethod
    def _sanitize_checkpoint(
        checkpoint: dict[str, object],
        drop_keys: set[str],
        hash_keys: set[str],
        sensitive_keys: set[str],
    ) -> None:
        """Apply the same drop/hash/sensitive policies to a checkpoint payload."""
        for key in drop_keys:
            checkpoint.pop(key, None)
        for key in hash_keys:
            value = checkpoint.get(key)
            if value is not None:
                checkpoint[key] = hashlib.sha256(str(value).encode("utf-8")).hexdigest()
        for key in sensitive_keys:
            if checkpoint.get(key) is not None:
                checkpoint[key] = "[REDACTED]"

    @staticmethod
    def _hash_at_path(target: dict[str, Any], key: str) -> None:
        value = Logger._get_path(target, key)
        if value is None:
            return
        hashed = hash_sensitive_value(value)
        Logger._set_path_direct(target, key, hashed)

    @staticmethod
    def _redact_at_path(target: dict[str, Any], key: str) -> None:
        if Logger._get_path(target, key) is not None:
            Logger._set_path_direct(target, key, "[REDACTED]")
        """Create a sanitized copy by delegating to the standalone sanitize_event."""
        return sanitize_event(ctx)

    @staticmethod
    def _get_path(target: dict[str, Any], key: str) -> Any:
        if "." not in key:
            return target.get(key)
        parts = key.split(".")
        current = target
        for part in parts:
            if not isinstance(current, dict) or part not in current:
                return None
            current = current[part]
        return current

    @staticmethod
    def _set_path(target: dict[str, Any], key: str, value: Any, policy: str = LastWins) -> None:
        if "." not in key:
            Logger._apply_duplicate(target, key, value, policy)
            return
        parts = key.split(".")
        current = target
        for part in parts[:-1]:
            child = current.get(part)
            if not isinstance(child, dict):
                child = {}
                current[part] = child
            current = child
        Logger._apply_duplicate(current, parts[-1], value, policy)

    @staticmethod
    def _apply_duplicate(target: dict[str, Any], key: str, value: Any, policy: str) -> None:
        apply_duplicate(target, key, value, policy)

    @staticmethod
    def _delete_path(target: dict[str, Any], key: str) -> None:
        if "." not in key:
            target.pop(key, None)
            return
        parts = key.split(".")
        current = target
        for part in parts[:-1]:
            child = current.get(part)
            if not isinstance(child, dict):
                return
            current = child
        current.pop(parts[-1], None)
