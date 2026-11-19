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
import { String, SensitiveString, _now } from '../src/core/event.ts';

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
import { String, SensitiveString, resetClock } from '../src/core/event.ts';

// Canonical snapshot for the deterministic checkpoint event below. Ids and
// timestamps are pinned via setIdGenerator/setClock, so this literal is stable.
const EXPECTED_CHECKPOINT_EVENT =
  '{"schema_version":"v1","event_version":"v1","event_id":"00000000-0000-7000-8000-000000000002","request_id":"00000000-0000-7000-8000-000000000002","timestamp":"2023-11-14T22:13:20.000Z","service":"test","event":"checkpoint","kind":"event","level":"info","event_state":"finished","outcome":"success","environment":"test","runtime":"node","finished_at":"2023-11-14T22:13:20.000Z","attrs":{"secret":"[REDACTED]"},"checkpoints":[{"name":"started","at_ms":0}]}';
import { String, SensitiveString, _now, resetClock } from '../src/core/event.ts';

describe('Testkit boundaries', () => {
  it('creates test loggers and captures emitted events', async () => {
    const result = testLogger({ service: 'test-service' });
    await result.logger.info('hello', String('key', 'value'));
    assert.equal(result.sink.getLength(), 1);
    const captured = await capture(async logger => { await logger.info('captured'); });
    assert.equal(captured.length, 1);
  });

  it('honors the supported testLogger config fields and defaults the rest', async () => {
    const configured = testLogger({ service: 'custom-service', environment: 'staging' });
    assert.equal(configured.logger.getConfig().service, 'custom-service');
    assert.equal(configured.logger.getConfig().environment, 'staging');
    await configured.logger.info('configured');
    assert.equal(configured.sink.getLength(), 1);
    assertEvent(configured.sink.getEvents()[0], 'service', 'custom-service');
    assertEvent(configured.sink.getEvents()[0], 'environment', 'staging');

    const defaulted = testLogger();
    assert.equal(defaulted.logger.getConfig().service, 'test');
    assert.equal(defaulted.logger.getConfig().environment, 'test');

    const partial = testLogger({ environment: 'ci' });
    assert.equal(partial.logger.getConfig().service, 'test');
    assert.equal(partial.logger.getConfig().environment, 'ci');
  });

  it('tracks testkit events and supports assertions', async () => {
    clearEvents();
    setClock(() => 1700000000000);
    setIdGenerator(() => '00000000-0000-7000-8000-000000000002');
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
    // Compare against a committed literal snapshot rather than a value derived
    // from the event under test; the latter is a guaranteed tautology.
    snapshotEvent(encoded, EXPECTED_CHECKPOINT_EVENT);
    assert.throws(() => assertEvent(encoded, 'missing', true), /not found/);
    assert.throws(() => assertEvent(encoded, 'event', 'other'), /expected/);
    assert.throws(() => assertRedacted(encoded, 'event'), /expected/);
    assert.throws(() => assertHasCheckpoint(encoded, 'missing'), /not found/);
    assert.throws(() => snapshotEvent(encoded, '{}'), /does not match/);
    clearEvents();
    assert.equal(events().length, 0);
    assert.equal(lastEvent(), undefined);
    resetForTest();
    resetClock();
  });

  it('isolates captured events between independent testkit instances', async () => {
    clearEvents();
    const kitA = testkit();
    const kitB = testkit();

    await kitA.logger.info('from-a');
    await kitB.logger.info('from-b');

    assert.equal(kitA.events().length, 1);
    assert.equal(kitB.events().length, 1);
    assert.deepEqual(kitA.events(), kitA.sink.getEvents());
    assert.deepEqual(kitB.events(), kitB.sink.getEvents());

    const aEvents = kitA.events();
    kitB.clearEvents();
    assert.deepEqual(kitB.events(), []);
    assert.deepEqual(kitA.events(), aEvents, 'clearing one kit must not wipe another');

    kitA.clearEvents();
    assert.deepEqual(kitA.events(), []);
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

  it('resetForTest restores the real clock after a fake clock is installed', () => {
    setClock(() => 300);
    assert.equal(_now(), 300);
    const before = Date.now();
    resetForTest();
    const after = Date.now();
    const now = _now();
    assert.ok(now >= before && now <= after, `expected a real timestamp after reset, got ${now}`);
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
  });

  it('rejects invalid clock values instead of silently ignoring them', () => {
    for (const invalid of [{}, { now: 123 }, null, undefined, 42, 'clock'] as never[]) {
      assert.throws(() => setClock(invalid), /expected a function or an object with a now\(\) method/);
    }
    // Valid clocks are still accepted and returned.
    const clock = new FakeClock(10);
    assert.equal(setClock(clock), clock);
    assert.equal(setClock(() => 20)(), 20);
    resetForTest();
    const clock = new FakeClock(500);
    // The helper returns the clock it installed, and a real FakeClock must be
    // wired into the core clock so that _now() reflects its value.
    assert.equal(setClock(clock), clock);
    assert.equal(_now(), 500);
    clock.advance(25);
    assert.equal(_now(), 525);
    resetClock();
  });

  it('imports only the type actually used from core/event', () => {
    const source = fs.readFileSync(
      path.resolve(import.meta.dirname, '../src/testkit/helpers.ts'),
      'utf-8',
    );
    const typeImport = source.match(/^import type \{([^}]*)\} from '\.\.\/core\/event\.ts';$/m);
    assert.ok(typeImport, 'expected a single-line type-only import from ../core/event.ts');
    const importedNames = typeImport![1].split(',').map(name => name.trim()).filter(Boolean);
    assert.deepEqual(importedNames, ['Params']);
    assert.ok(!importedNames.includes('Event'));
    assert.ok(!importedNames.includes('Attr'));
  });
});
