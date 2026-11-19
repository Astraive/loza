import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  testLogger, capture, testkit, events, lastEvent, clearEvents,
  assertEvent, assertAttr, expectEvent, expectAttr, assertRedacted,
  assertHasCheckpoint, snapshotEvent, MockSink, FakeClock,
  setClock, setIdGenerator, resetForTest, goldenTest, conformanceSuite,
} from '../src/testkit/helpers.ts';
import { String, SensitiveString } from '../src/core/event.ts';

// Each golden-file check must use a fresh, unique directory. A path derived
// only from process.pid can collide with a stale file left by an earlier run
// (or with a parallel worker), which would make the first comparison fail
// against unrelated golden content.
function freshGoldenPath(): { dir: string; filePath: string } {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'loza-golden-'));
  return { dir, filePath: path.join(dir, 'golden.json') };
}

function runGoldenFileChecks(filePath: string): void {
  assert.equal(goldenTest(filePath, '{"event":"one"}'), true);
  assert.equal(goldenTest(filePath, '{"event":"one"}'), true);
  assert.equal(goldenTest(filePath, '{"event":"two"}'), false);
}

describe('Testkit boundaries', () => {
  it('creates test loggers and captures emitted events', async () => {
    const result = testLogger({ service: 'test-service' });
    await result.logger.info('hello', String('key', 'value'));
    assert.equal(result.sink.getLength(), 1);
    const captured = await capture(async logger => { await logger.info('captured'); });
    assert.equal(captured.length, 1);
  });

  it('tracks testkit events and supports assertions', async () => {
    clearEvents();
    const kit = testkit();
    const event = kit.logger.startEvent({ event: 'checkpoint' });
    event.checkpoint('started');
    event.enrich(SensitiveString('secret', 'value'));
    event.finish('success');
    await kit.logger.emit(event);
    const encoded = kit.lastEvent();
    assert.ok(encoded);
    assert.equal(events().length, 1);
    assert.equal(kit.events().length, 1);
    assertEvent(encoded, 'event', 'checkpoint');
    assertAttr(encoded, 'event', 'checkpoint');
    expectEvent(encoded, 'service', 'test');
    expectAttr(encoded, 'event', 'checkpoint');
    assertRedacted(encoded, 'attrs.secret');
    assertHasCheckpoint(encoded, 'started');
    snapshotEvent(encoded, JSON.stringify(JSON.parse(encoded)));
    assert.throws(() => assertEvent(encoded, 'missing', true), /not found/);
    assert.throws(() => assertEvent(encoded, 'event', 'other'), /expected/);
    assert.throws(() => assertRedacted(encoded, 'event'), /expected/);
    assert.throws(() => assertHasCheckpoint(encoded, 'missing'), /not found/);
    assert.throws(() => snapshotEvent(encoded, '{}'), /does not match/);
    clearEvents();
    assert.equal(events().length, 0);
    assert.equal(lastEvent(), undefined);
  });

  it('provides mock sinks and deterministic clocks and IDs', async () => {
    const sink = new MockSink();
    await sink.write('one');
    sink.failNext = true;
    await assert.rejects(() => sink.write('two'), /mock write failure/);
    assert.deepEqual(sink.getEvents(), ['one']);
    sink.clear();
    assert.deepEqual(sink.getEvents(), []);

    const clock = new FakeClock(100);
    assert.equal(clock.now(), 100);
    clock.advance(25);
    assert.equal(clock.now(), 125);
    clock.setTime(200);
    assert.equal(clock.now(), 200);
    const previous = setClock(clock);
    assert.equal(typeof previous, 'object');
    setClock(() => 300);
    resetForTest();
    setIdGenerator(() => 'fixed-id');
    resetForTest();
  });

  it('writes and compares golden files and reports conformance availability', () => {
    const { dir, filePath } = freshGoldenPath();
    try {
      runGoldenFileChecks(filePath);
      assert.deepEqual(conformanceSuite(), { name: 'loza-js-conformance', status: 'available' });
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });

  it('golden checks ignore a stale PID-keyed temp file', () => {
    // Reproduce the reported trigger: a stale file at the old deterministic,
    // PID-keyed path holding unrelated golden content. Before the fix the
    // boundary test compared against it and failed flakily.
    const stalePath = path.join(os.tmpdir(), `loza-golden-${process.pid}.json`);
    fs.writeFileSync(stalePath, '{"event":"unrelated"}\n');
    try {
      const { dir, filePath } = freshGoldenPath();
      try {
        assert.notEqual(filePath, stalePath);
        runGoldenFileChecks(filePath);
      } finally {
        fs.rmSync(dir, { recursive: true, force: true });
      }
    } finally {
      fs.rmSync(stalePath, { force: true });
    }
  });
  it('handles missing checkpoint collections and non-object nested paths', () => {
    assert.throws(() => assertHasCheckpoint('{}', 'missing'), /not found/);
    assert.throws(() => assertEvent('{"value":1}', 'value.child', true), /not found/);
    const clock = setClock({} as never);
    assert.deepEqual(clock, {});
  });
});
