import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { Event } from '../src/core/event.ts';
import { EventView } from '../src/core/event-view.ts';

function makeEvent(): Event {
  const event = new Event({ event: 'immutability', service: 'svc' }, 'svc', 'test');
  event.attrs = {
    nested: { deep: { value: 1 } },
    list: [1, 2, { inside: true }],
  };
  event.checkpoints = [{ name: 'db', at_ms: 2, attrs: { query: 'select 1' } }];
  event.processes = [
    { step: 1, name: 'worker', started_at_ms: 1, ended_at_ms: 2, duration_ms: 1, attrs: { code: 0 } },
  ];
  event.groups = [
    { name: 'group', started_at_ms: 1, ended_at_ms: 2, duration_ms: 1, attrs: { phase: 'init' } },
  ];
  event.timers = [{ name: 'timer', duration_ms: 1, attrs: { tick: 1 } }];
  event.error = { type: 'Error', message: 'boom', stack: 'stack', retryable: true };
  return event;
}

describe('EventView immutability', () => {
  it('deep-freezes nested attrs and arrays', () => {
    const view = new EventView(makeEvent());

    assert.equal(Object.isFrozen(view.attrs), true);
    assert.equal(Object.isFrozen(view.attrs.nested), true);
    assert.equal(Object.isFrozen(view.attrs.nested.deep), true);
    assert.equal(Object.isFrozen(view.attrs.list), true);
    assert.equal(Object.isFrozen((view.attrs.list as any)[2]), true);
    assert.throws(() => { view.attrs.nested.deep.value = 99; }, TypeError);
    assert.throws(() => { (view.attrs.list as any).push(3); }, TypeError);
  });

  it('deep-freezes checkpoint entries and their attrs', () => {
    const view = new EventView(makeEvent());

    assert.equal(Object.isFrozen(view.checkpoints), true);
    assert.equal(Object.isFrozen(view.checkpoints[0]), true);
    assert.equal(Object.isFrozen(view.checkpoints[0].attrs), true);
    assert.throws(() => { view.checkpoints[0].name = 'mutated'; }, TypeError);
    assert.throws(() => { view.checkpoints[0].attrs!.query = 'mutated'; }, TypeError);
  });

  it('deep-freezes process, group, and timer entries and their attrs', () => {
    const view = new EventView(makeEvent());

    for (const entry of [...view.processes, ...view.groups, ...view.timers]) {
      assert.equal(Object.isFrozen(entry), true);
      assert.equal(Object.isFrozen(entry.attrs), true);
    }
    assert.throws(() => { view.processes[0].attrs!.code = 1; }, TypeError);
    assert.throws(() => { view.groups[0].attrs!.phase = 'done'; }, TypeError);
    assert.throws(() => { view.timers[0].attrs!.tick = 2; }, TypeError);
  });

  it('freezes the error object and keeps it a defensive copy', () => {
    const event = makeEvent();
    const view = new EventView(event);

    assert.notEqual(view.error, event.error);
    assert.equal(Object.isFrozen(view.error), true);
    assert.throws(() => { view.error!.message = 'mutated'; }, TypeError);
    assert.equal(view.error!.message, 'boom');
    assert.equal(event.error!.message, 'boom');
  });

  it('keeps a null error as null', () => {
    const event = new Event({ event: 'no-error', service: 'svc' }, 'svc', 'test');
    assert.equal(new EventView(event).error, null);
  });

  it('does not freeze or mutate the source event through the view', () => {
    const event = makeEvent();
    const view = new EventView(event);

    assert.notEqual(view.attrs.nested, event.attrs.nested);
    assert.notEqual(view.checkpoints[0], event.checkpoints[0]);
    assert.equal(Object.isFrozen(event.attrs.nested), false);
    assert.equal(Object.isFrozen(event.checkpoints[0]), false);
    assert.equal(Object.isFrozen(event.error), false);

    // The original event remains mutable and the view is an independent snapshot.
    event.attrs.nested.deep.value = 5;
    event.checkpoints[0].name = 'changed';
    assert.equal(view.attrs.nested.deep.value, 1);
    assert.equal(view.checkpoints[0].name, 'db');
  });

  it('handles cyclic attribute values without infinite recursion', () => {
    const event = new Event({ event: 'cyclic', service: 'svc' }, 'svc', 'test');
    const cyclic: any = { name: 'root' };
    cyclic.self = cyclic;
    event.attrs = { cyclic };

    const view = new EventView(event);
    assert.equal(Object.isFrozen(view.attrs.cyclic), true);
    assert.equal((view.attrs.cyclic as any).self, view.attrs.cyclic);
  });
});
