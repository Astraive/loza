"""
Test panic safety: graceful error handling without unhandled exceptions.

LOZA must guarantee:
- Errors are returned cleanly, not raise unhandled exceptions
- Invalid inputs handled gracefully
- Edge cases don't crash the SDK
"""

from __future__ import annotations

import json

import pytest


import loza


def test_oversized_event_handled_gracefully() -> None:
    """Verify oversized events are rejected cleanly."""
    logger = loza.New(loza.Test("test"))
    ctx = logger.start_event(loza.Params(event="test"))

    # Try to add extremely large attribute
    huge_value = "x" * (10 * 1024 * 1024)  # 10MB string
    logger.enrich(ctx, loza.String("huge", huge_value))
    logger.finish(ctx, "success")

    # Should either emit empty (dropped) or raise ValueError, not crash
    try:
        payload = logger.emit(ctx)
        # If it emits, that's fine
        if payload:
            event = json.loads(payload)
            assert event is not None
    except ValueError:
        # Validation error is acceptable
        pass


def test_invalid_event_name_handled() -> None:
    """Verify invalid event names are rejected with a typed error, not a panic."""
    logger = loza.New(loza.Test("test"))

    ctx = logger.start_event(loza.Params(event=" "))
    logger.finish(ctx, "success")

    # A whitespace-only name is invalid: emit must surface the known, typed
    # validation error. Any other exception type (e.g. a panic-derived one)
    # propagates out of pytest.raises and fails the test.
    with pytest.raises(loza.EventValidationError, match=r'field "event" must be a non-empty string'):
        logger.emit(ctx)

    # A rejected event must never be reported as successfully delivered.
    assert not ctx.emitted
    assert ctx.event_state == "failed_validation"


def test_invalid_event_name_never_raises_arbitrary_exception() -> None:
    """Regression: invalid names must raise EventValidationError, never an arbitrary error."""
    for name in (" ", "", "\t\n"):
        logger = loza.New(loza.Test("test"))
        ctx = logger.start_event(loza.Params(event=name))
        logger.finish(ctx, "success")

        try:
            logger.emit(ctx)
        except loza.EventValidationError:
            pass
        except BaseException as exc:  # pragma: no cover - only reached on regression
            pytest.fail(f"invalid event name {name!r} raised unexpected {type(exc).__name__}: {exc}")
        else:
            pytest.fail(f"invalid event name {name!r} was silently accepted")

        assert not ctx.emitted
        assert ctx.event_state == "failed_validation"


def test_sink_write_failure_doesnt_crash() -> None:
    """Verify sink failures don't crash the SDK."""

    class FailingSink:
        def write(self, payload: str) -> None:
            raise IOError("Sink is broken")

        def flush(self) -> None:
            pass

        def close(self) -> None:
            pass

    logger = loza.New(loza.Test("test").with_sink(FailingSink()))
    ctx = logger.start_event(loza.Params(event="test"))
    logger.finish(ctx, "success")

    # Sink exceptions stay contained, but the observable event state and return
    # value must not claim successful delivery.
    assert logger.emit(ctx) == ""
    assert not ctx.emitted
    assert ctx.event_state == "delivery_failed"


def test_invalid_duplicate_policy_rejected() -> None:
    """A None duplicate policy must be handled gracefully, not panic.

    ``None`` means "no override" and falls back to the default policy. The
    previous test passed unconditionally on that path, so assert the logger
    still emits a valid event instead of silently letting anything through.
    """
    logger = loza.New(loza.Test("test").with_duplicate_policy(None))
    ctx = logger.start_event(loza.Params(event="test"))
    logger.finish(ctx, "success")

    payload = logger.emit(ctx)
    assert payload, "None duplicate policy must not suppress event emission"
    assert json.loads(payload) is not None


def test_invalid_sampler_rejected() -> None:
    """A None sampler must fall back to the default sampler, not panic."""
    logger = loza.New(loza.Test("test").with_sampler(None))
    ctx = logger.start_event(loza.Params(event="test"))
    logger.finish(ctx, "success")

    payload = logger.emit(ctx)
    assert payload, "None sampler must not suppress event emission"
    assert json.loads(payload) is not None


def test_invalid_schema_rejected() -> None:
    """A None schema must fall back to the default schema, not panic."""
    logger = loza.New(loza.Test("test").with_schema(None))
    ctx = logger.start_event(loza.Params(event="test"))
    logger.finish(ctx, "success")

    payload = logger.emit(ctx)
    assert payload, "None schema must not suppress event emission"
    assert json.loads(payload) is not None


def test_invalid_redactor_rejected() -> None:
    """A None redactor must fall back to the default redactor, not panic."""
    logger = loza.New(loza.Test("test").with_redactor(None))
    ctx = logger.start_event(loza.Params(event="test"))
    logger.finish(ctx, "success")

    payload = logger.emit(ctx)
    assert payload, "None redactor must not suppress event emission"
    assert json.loads(payload) is not None


def test_none_config_overrides_resolve_to_safe_defaults() -> None:
    """Regression: None overrides must resolve to working defaults.

    The four invalid-config tests above used to pass on an unconditional
    no-raise path, so a logger left with a None sampler, redactor or schema
    would only panic later during emit. Assert the defaults are actually
    resolved and that the full start/finish/emit pipeline still works.
    """
    logger = loza.New(
        loza.Test("test").with_duplicate_policy(None).with_sampler(None).with_schema(None).with_redactor(None)
    )

    assert logger._config.sampler is not None
    assert logger._config.redactor is not None
    assert logger._config.schema is not None
    assert logger._config.duplicate_policy is not None

    ctx = logger.start_event(loza.Params(event="test"))
    logger.finish(ctx, "success")
    payload = logger.emit(ctx)
    assert payload
    assert json.loads(payload) is not None


@pytest.mark.parametrize(
    "option",
    ["with_sampler", "with_redactor", "with_schema"],
)
def test_non_callable_config_values_are_rejected(option: str) -> None:
    """Genuinely invalid config values must raise a known error, not panic.

    Unlike ``None`` (which means "use the default"), a non-callable value can
    never be a valid sampler/redactor/schema and must be surfaced as an error.
    """
    with pytest.raises((TypeError, ValueError, AttributeError)):
        logger = loza.New(getattr(loza.Test("test"), option)("not-callable"))
        ctx = logger.start_event(loza.Params(event="test"))
        logger.finish(ctx, "success")
        logger.emit(ctx)


def test_finish_twice_handled() -> None:
    """Verify calling finish() twice is handled."""
    logger = loza.New(loza.Test("test"))
    ctx = logger.start_event(loza.Params(event="test"))

    logger.finish(ctx, "success")

    # Calling finish again must raise a known error (not panic). Use
    # pytest.raises so a second finish() that silently succeeds fails the test.
    with pytest.raises(loza.EventAlreadyFinishedError, match="already finished"):
        logger.finish(ctx, "error")


def test_emit_before_finish_handled() -> None:
    """Verify emitting before finish is allowed or errors cleanly."""
    logger = loza.New(loza.Test("test"))
    ctx = logger.start_event(loza.Params(event="test"))

    # Emitting without finish may be allowed or error
    try:
        payload = logger.emit(ctx)
        # Python SDK may emit partial events
        if payload:
            event = json.loads(payload)
            assert event is not None
    except (ValueError, RuntimeError, KeyError):
        # Known error is acceptable
        pass


def test_null_attribute_values_handled() -> None:
    """Verify null/None values in attributes are handled."""
    logger = loza.New(loza.Test("test"))
    ctx = logger.start_event(loza.Params(event="test"))

    # Try to enrich with None value
    try:
        logger.enrich(ctx, loza.String("attr", None))
        logger.finish(ctx, "success")
        logger.emit(ctx)
        # Should handle without crashing
    except (TypeError, ValueError):
        # Known error is acceptable
        pass


def test_special_characters_in_attributes() -> None:
    """Verify special characters are handled."""
    logger = loza.New(loza.Test("test"))
    ctx = logger.start_event(loza.Params(event="test"))

    special_values = [
        "\x00null byte",
        "\uffff unicode",
        '\\"quotes\\"',
        "\\nnewlines\\n",
        "{}[]()\"quotes'",
    ]

    for value in special_values:
        try:
            logger.enrich(ctx, loza.String("attr", value))
        except (ValueError, TypeError):
            pass

    logger.finish(ctx, "success")
    payload = logger.emit(ctx)
    # Should emit without crashing
    if payload:
        event = json.loads(payload)
        assert event is not None


def test_deeply_nested_attributes() -> None:
    """Verify deeply nested attribute structures are handled."""
    logger = loza.New(loza.Test("test"))
    ctx = logger.start_event(loza.Params(event="test"))

    # Python SDK may not support nested objects directly
    # But it should handle without crashing
    try:
        for i in range(1000):
            logger.enrich(ctx, loza.String(f"attr_{i}", f"value_{i}"))
        logger.finish(ctx, "success")
        logger.emit(ctx)
        # Should complete without crashing
    except (ValueError, RuntimeError, MemoryError):
        # Known errors are acceptable
        pass
