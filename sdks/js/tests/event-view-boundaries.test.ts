import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { Event, SensitiveString, HashString } from '../src/core/event.ts';
import { EventView } from '../src/core/event-view.ts';

function makeEventWithRedactionKeys(): Event {
  const event = new Event({ event: 'view.redaction', service: 'svc' }, 'svc', 'test');
  event.enrich(
    SensitiveString('password', 'hunter2'),
    HashString('token', 'abc123'),
  );
  return event;
}

describe('EventView redaction set boundaries', () => {
  it('copies sensitive and hash key sets instead of exposing the event\'s live sets', () => {
    const event = makeEventWithRedactionKeys();
    const view = new EventView(event);

    assert.deepEqual([...view.sensitiveKeys], ['password']);
    assert.deepEqual([...view.hashKeys], ['token']);

    // ReadonlySet is compile-time only; a consumer can mutate at runtime.
    const mutableViewSensitive = view.sensitiveKeys as Set<string>;
    const mutableViewHash = view.hashKeys as Set<string>;
    mutableViewSensitive.delete('password');
    mutableViewSensitive.add('injected');
    mutableViewHash.delete('token');
    mutableViewHash.add('injected');

    // The original Event's redaction config must remain untouched.
    assert.deepEqual([...event.sensitiveKeys], ['password']);
    assert.deepEqual([...event.hashKeys], ['token']);
  });

  it('does not let later event mutations leak into an existing view', () => {
    const event = makeEventWithRedactionKeys();
    const view = new EventView(event);

    (event.sensitiveKeys as Set<string>).delete('password');
    (event.hashKeys as Set<string>).add('later');

    assert.deepEqual([...view.sensitiveKeys], ['password']);
    assert.deepEqual([...view.hashKeys], ['token']);
  });
});
