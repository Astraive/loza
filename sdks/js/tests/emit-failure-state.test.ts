import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { Logger } from '../src/core/logger.ts';
import { MemorySink } from '../src/sinks/standard-sinks.ts';

/**
 * Regression coverage for MASS-0551 / issue #368.
 *
 * `emit()` marks the event as emitted before running the sanitize -> encode ->
 * redact -> serialize -> deliver pipeline. If any stage before delivery throws,
 * the event must be moved to a terminal failed state (`failed_validation`)
 * rather than being left stuck in `emitting` with `_emitted` already true.
 */

function makeEvent(logger: Logger) {
  const ctx = logger.startEvent({ event: 'test.event' });
  ctx.finish('success');
  return ctx;
}

describe('emit failure state', () => {
  it('marks the event failed_validation when schema.encode throws', async () => {
    const sink = new MemorySink();
    const logger = new Logger({
      service: 'svc',
      sink,
      schema: {
        encode() {
          throw new Error('encode boom');
        },
      },
    });
    const ctx = makeEvent(logger);

    await assert.rejects(() => logger.emit(ctx), /encode boom/);
    assert.equal(ctx.getEventState(), 'failed_validation');
    assert.equal(sink.getLength(), 0);

    // A later emit is a no-op (idempotency guard) and must not resurrect the
    // event from an intermediate `emitting` state.
    assert.equal(await logger.emit(ctx), null);
    assert.equal(ctx.getEventState(), 'failed_validation');
  });

  it('marks the event failed_validation when the redactor throws', async () => {
    const sink = new MemorySink();
    const logger = new Logger({
      service: 'svc',
      sink,
      redactor() {
        throw new Error('redactor boom');
      },
    });
    const ctx = makeEvent(logger);

    await assert.rejects(() => logger.emit(ctx), /redactor boom/);
    assert.equal(ctx.getEventState(), 'failed_validation');
    assert.equal(sink.getLength(), 0);
  });

  it('marks the event failed_validation when serialization throws', async () => {
    const sink = new MemorySink();
    // A BigInt value makes JSON.stringify throw while encoding the payload.
    const logger = new Logger({
      service: 'svc',
      sink,
      schema: {
        encode() {
          return { bad: 1n };
        },
      },
    });
    const ctx = makeEvent(logger);

    await assert.rejects(() => logger.emit(ctx), /BigInt/);
    assert.equal(ctx.getEventState(), 'failed_validation');
    assert.equal(sink.getLength(), 0);
  });

  it('still marks delivery failures as delivery_failed', async () => {
    const logger = new Logger({
      service: 'svc',
      sink: {
        name: () => 'failing',
        write() {
          throw new Error('write boom');
        },
        flush() {},
        close() {},
      },
    });
    const ctx = makeEvent(logger);

    assert.equal(await logger.emit(ctx), null);
    assert.equal(ctx.getEventState(), 'delivery_failed');
  });

  it('leaves successful emits in the emitted state', async () => {
    const sink = new MemorySink();
    const logger = new Logger({ service: 'svc', sink });
    const ctx = makeEvent(logger);

    assert.ok(await logger.emit(ctx));
    assert.equal(ctx.getEventState(), 'emitted');
    assert.equal(sink.getLength(), 1);
  });
});
