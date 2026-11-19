import { afterEach, it } from 'node:test';
import assert from 'node:assert/strict';
import * as loza from '../src/index.ts';

afterEach(() => {
  loza.reset();
  // Injected test globals (ID generator, clock, captured events) must be torn
  // down after every test, including tests that fail before their body ends.
  loza.resetForTest();
});

it('covers testing and conformance helpers', async () => {
  const events = await loza.capture(async (logger) => {
    const event = logger.startEvent({ event: 'capture.event' });
    logger.append(event, loza.string('family', 'testkit'));
    logger.checkpoint(event, 'captured');
    logger.finish(event, 'success');
    await logger.emit(event);
  });
  assert.equal(events.length, 1);
  loza.assertEvent(events[0], 'attrs.family', 'testkit');
  loza.assertHasCheckpoint(events[0], 'captured');
  loza.snapshotEvent(events[0], JSON.stringify(JSON.parse(events[0])));

  const mock = new loza.MockSink();
  await mock.write('{"ok":true}');
  assert.equal(mock.getEvents().length, 1);
  const clock = new loza.FakeClock(1000);
  clock.advance(50);
  assert.equal(clock.now(), 1050);
  loza.setIdGenerator(() => 'evt_fixed');
  const deterministicLogger = loza.createLoza({ service: 'idgen', sink: new loza.MemorySink() });
  const deterministic = deterministicLogger.startEvent({ event: 'deterministic.id' });
  assert.equal(deterministic.eventId, 'evt_fixed');
});

// Regression: a test body that aborts before any in-body cleanup (e.g. a failed
// assertion) must not leave the injected ID generator installed for later tests.
it('aborts its body before in-body cleanup after injecting an ID generator', () => {
  loza.setIdGenerator(() => 'evt_leaked');
  try {
    assert.equal(1, 2, 'simulated early assertion failure');
  } catch {
    // Body aborts here; the generator stays injected until afterEach runs.
  }
});

it('does not leak the injected ID generator into later tests', () => {
  const logger = loza.createLoza({ service: 'leak-check', sink: new loza.MemorySink() });
  const event = logger.startEvent({ event: 'leak.check' });
  assert.notEqual(event.eventId, 'evt_leaked');
});

// Regression: a failed test must not leak an injected clock either.
it('aborts its body after injecting a clock', () => {
  loza.setClock(() => 424242);
  try {
    assert.equal(1, 2, 'simulated early assertion failure');
  } catch {
    // Body aborts here; the clock stays injected until afterEach runs.
  }
});

it('does not leak the injected clock into later tests', () => {
  const logger = loza.createLoza({ service: 'clock-leak-check', sink: new loza.MemorySink() });
  const event = logger.startEvent({ event: 'clock.leak.check' });
  // With a real clock the start time is the current wall clock in ms, not the
  // injected fixed value (424242) from the preceding test.
  assert.notEqual(event.startedAt, 424242);
  assert.ok(event.startedAt > 1_000_000_000_000);
});
