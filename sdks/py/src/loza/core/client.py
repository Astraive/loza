"""Compatibility import path for the canonical ``loza.core.logger.Logger``.

This module used to hold a second, diverged copy of the ``Logger``
implementation. The package never used it (``loza.core`` exports ``Logger``
from ``.logger`` and ``config.new_client`` instantiates ``.logger.Logger``),
so fixes made to the canonical implementation never took effect for callers
importing from ``loza.core.client``.

The legacy import path is preserved as a thin re-export so that there is a
single implementation of ``Logger`` and no public API is broken.
"""

from __future__ import annotations
    def shutdown(self, timeout: float = 30.0) -> None:
        if self._pipeline is not None:
            # Pipeline.close() flushes and closes every sink itself; closing them
            # again here would double-close non-idempotent sinks.
            self._pipeline.close(timeout=timeout)
            self._pipeline = None
        else:
            self.flush(timeout=timeout)
            for sink in self._config.sinks:
                close = getattr(sink, "close", None)
                if callable(close):
                    close()

    def debug(self, message: str, **attrs: Any) -> str:
        return self._emit_immediate("debug", message, **attrs)

    def info(self, message: str, **attrs: Any) -> str:
        return self._emit_immediate("info", message, **attrs)

    def warn(self, message: str, **attrs: Any) -> str:
        return self._emit_immediate("warn", message, **attrs)

    def error(self, message: str, **attrs: Any) -> str:
        return self._emit_immediate("error", message, **attrs)

    def fatal(self, message: str, **attrs: Any) -> str:
        return self._emit_immediate("fatal", message, **attrs)

    def _emit_immediate(self, level: str, message: str, **attrs: Any) -> str:
        ctx = self.start_event(Params(event=f"log.{level}", kind="log", message=message, level=level))
        self.enrich(ctx, **attrs)
        self.finish(ctx, "success")
        return self.emit(ctx)

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
            snapshot.attrs = {key: value for key, value in checkpoint.items() if key not in {"name", "at_ms"}}
            payload = self._config.schema.encode(snapshot)
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
        total = sum(len(bucket) for bucket in buckets)
        if total <= max_attr_count:
            return

        remaining = max_attr_count
        truncated = False
        for bucket in buckets:
            if remaining <= 0:
                if bucket:
                    bucket.clear()
                    truncated = True
                continue
            if len(bucket) > remaining:
                kept = list(bucket.items())[:remaining]
                bucket.clear()
                bucket.update(kept)
                truncated = True
            remaining -= len(bucket)

        if truncated and "_truncated" not in ctx.attrs:
            ctx.attrs["_truncated"] = True

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

from .duplicate_policy import apply_duplicate
from .logger import Logger

__all__ = ["Logger"]
