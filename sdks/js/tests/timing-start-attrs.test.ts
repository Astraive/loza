import { afterEach, it } from 'node:test';
import assert from 'node:assert/strict';
import * as loza from '../src/index.ts';

afterEach(() => {
  loza.reset();
});

function newContext(): any {
  const sink = new loza.MemorySink();
  loza.configure(loza.test('catalog').withSink(sink));
  const logger = loza.createLoza({ service: 'catalog', sink });
  return logger.startEvent({ event: 'timing.start.attrs', kind: 'test' });
}

it('persists start-time attrs passed through withProcess/withGroup/withTimer', () => {
  const ctx = newContext();

  loza.withProcess(ctx, 'wrapped.process', () => {}, loza.string('phase', 'init'), loza.int('attempt', 2));
  loza.withGroup(ctx, 'wrapped.group', () => {}, loza.string('phase', 'group-init'));
  loza.withTimer(ctx, 'wrapped.timer', () => {}, loza.string('cache', 'miss'));

  assert.equal(ctx.processes.length, 1);
  assert.deepEqual(ctx.processes[0].attrs, { phase: 'init', attempt: 2 });

  assert.equal(ctx.groups.length, 1);
  assert.deepEqual(ctx.groups[0].attrs, { phase: 'group-init' });

  assert.equal(ctx.timers.length, 1);
  assert.deepEqual(ctx.timers[0].attrs, { cache: 'miss' });
});

it('persists start-time attrs passed through the step/phase/span aliases', () => {
  const ctx = newContext();

  loza.step(ctx, 'alias.step', loza.string('src', 'step')).finish();
  loza.phase(ctx, 'alias.phase', loza.string('src', 'phase')).finish();
  loza.span(ctx, 'alias.span', loza.string('src', 'span')).stop();

  assert.deepEqual(ctx.processes[0].attrs, { src: 'step' });
  assert.deepEqual(ctx.groups[0].attrs, { src: 'phase' });
  assert.deepEqual(ctx.timers[0].attrs, { src: 'span' });
});

it('extracts status_code from start attrs and lets finish attrs override start attrs', () => {
  const ctx = newContext();

  loza.withProcess(ctx, 'wrapped.status', () => {}, loza.httpStatus(201), loza.string('k', 'start'));

  assert.equal(ctx.processes[0].status_code, 201);
  assert.deepEqual(ctx.processes[0].attrs, { k: 'start' });

  const handle = loza.startProcess(ctx, 'override.process', loza.string('k', 'start'), loza.string('only_start', 1));
  handle.finish(loza.string('k', 'finish'));

  const entry = ctx.processes[1];
  assert.equal(entry.attrs.k, 'finish');
  assert.equal(entry.attrs.only_start, 1);
});
