from __future__ import annotations

import json

import loza
from loza.core import event as event_module
from loza.core.hashing import secure_hash

HASH_RAW = "HASH_RAW"
# Sensitive values (including checkpoints) use a keyed HMAC, never a bare SHA-256.
EXPECTED_HASH = secure_hash(HASH_RAW)


def _checkpoint_attrs(immediate: bool) -> tuple[list[dict], loza.EventContext]:
    sink = loza.MemorySink()
    cfg = loza.Test("checkpoint-policy").with_sink(sink)
    cfg.checkpoint_emit_immediately = immediate
    logger = loza.New(cfg)
    ctx = logger.start_event(loza.Params(event="step"))
    logger.checkpoint(
        ctx,
        "db",
        loza.Attr("alpha", "SENSITIVE_RAW", sensitive=True),
        loza.Attr("beta", HASH_RAW, hash_value=True),
        loza.Attr("gamma", "DROP_RAW", drop=True),
        plain="visible",
    )
    logger.finish(ctx, "success")
    logger.emit(ctx)
    return [json.loads(event) for event in sink.events], ctx


def test_checkpoint_attr_flags_apply_in_normal_mode() -> None:
    payloads, ctx = _checkpoint_attrs(immediate=False)

    assert len(payloads) == 1
    checkpoint = payloads[0]["checkpoints"][0]
    assert checkpoint["alpha"] == "[REDACTED]"
    assert checkpoint["beta"] == EXPECTED_HASH
    assert "gamma" not in checkpoint
    assert checkpoint["plain"] == "visible"

    # Sanitization must not mutate the original event context.
    raw = ctx.checkpoints[0]
    assert raw["alpha"] == "SENSITIVE_RAW"
    assert raw["beta"] == HASH_RAW
    assert raw["gamma"] == "DROP_RAW"


def test_checkpoint_attr_flags_apply_in_immediate_mode() -> None:
    payloads, _ = _checkpoint_attrs(immediate=True)

    assert len(payloads) == 2
    checkpoint_event = payloads[0]
    assert checkpoint_event["event"] == "checkpoint.db"
    attrs = checkpoint_event["attrs"]
    assert attrs["alpha"] == "[REDACTED]"
    assert attrs["beta"] == EXPECTED_HASH
    assert "gamma" not in attrs
    assert attrs["plain"] == "visible"


def test_checkpoint_uses_previously_registered_policy_keys() -> None:
    sink = loza.MemorySink()
    logger = loza.New(loza.Test("checkpoint-policy").with_sink(sink))
    ctx = logger.start_event(loza.Params(event="step"))
    logger.enrich(ctx, loza.Attr("alpha", "x", sensitive=True))
    logger.checkpoint(ctx, "db", alpha="SENSITIVE_RAW")
    logger.finish(ctx, "success")

    payload = json.loads(logger.emit(ctx))
    assert payload["checkpoints"][0]["alpha"] == "[REDACTED]"


def test_standalone_sanitize_event_covers_checkpoints() -> None:
    ctx = loza.EventContext(service="svc", params=loza.Params(event="step"))
    ctx._drop_keys.add("gamma")
    ctx._hash_keys.add("beta")
    ctx._sensitive_keys.add("alpha")
    ctx.checkpoints.append(
        {"name": "db", "alpha": "SENSITIVE_RAW", "beta": HASH_RAW, "gamma": "DROP_RAW"}
    )

    safe = event_module.sanitize_event(ctx)

    checkpoint = safe.checkpoints[0]
    assert checkpoint["alpha"] == "[REDACTED]"
    assert checkpoint["beta"] == EXPECTED_HASH
    assert "gamma" not in checkpoint
    assert ctx.checkpoints[0]["alpha"] == "SENSITIVE_RAW"
