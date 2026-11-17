import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { Event } from '../src/core/event.ts';
import {
  getEvent, runWithEvent, withoutEvent,
} from '../src/core/context.ts';
import { lozaHttpMiddleware } from '../src/middleware/http.ts';

function makeEvent(name: string): Event {
  return new Event({ event: name, service: 'svc', requestId: name }, 'svc', 'test');
}

const tick = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

describe('context concurrency and lifecycle', () => {
  it('scopes concurrent async work with runWithEvent so contexts do not bleed', async () => {
    const events = [makeEvent('req-a'), makeEvent('req-b'), makeEvent('req-c')];

    const results = await Promise.all(events.map((ev) =>
      runWithEvent(ev, async () => {
        await tick();
        const before = getEvent();
        await tick();
        const after = getEvent();
        await tick();
        return { expected: ev, before, after };
      })));

    for (const result of results) {
      assert.equal(result.before, result.expected);
      assert.equal(result.after, result.expected);
    }
    // No event leaks into the caller once all scoped operations complete.
    assert.equal(getEvent(), undefined);
  });

  it('scopes each job in a long-lived async chain without leaking', async () => {
    const seen: Array<Event | undefined> = [];

    // One async function (one async chain) processes multiple jobs in sequence.
    // Because each job's context is scoped with run(), the next job never sees
    // a previous job's event (the enterWith behaviour that used to leak).
    async function worker() {
      for (const name of ['job-1', 'job-2', 'job-3']) {
        const ev = makeEvent(name);
        seen.push(await runWithEvent(ev, async () => {
          await tick();
          return getEvent();
        }));
        assert.equal(getEvent(), undefined);
      }
    }

    await worker();
    assert.deepEqual(seen.map((ev) => ev?.event), ['job-1', 'job-2', 'job-3']);
    assert.equal(getEvent(), undefined);
  });

  it('withoutEvent isolates work from the current event', () => {
    const ev = makeEvent('outer');
    runWithEvent(ev, () => {
      assert.equal(getEvent(), ev);
      withoutEvent(() => {
        assert.equal(getEvent(), undefined);
      });
      // The previous context is restored after withoutEvent returns.
      assert.equal(getEvent(), ev);
    });
    assert.equal(getEvent(), undefined);
  });

  it('middleware scopes context to downstream work and restores it on completion', () => {
    const middleware = lozaHttpMiddleware({ service: 'edge' });
    let downstreamEvent: Event | undefined;
    let eventAtResponseEnd: Event | undefined;

    const res = {
      statusCode: 200,
      end() {
        eventAtResponseEnd = getEvent();
        return 'ended';
      },
      on() {},
    };
    const req = { method: 'GET', url: '/ctx', headers: {}, socket: {} };

    middleware(req, res, () => {
      downstreamEvent = getEvent();
    });

    assert.ok(downstreamEvent);
    assert.equal(downstreamEvent?.event, 'GET /ctx');

    // Completing the response triggers finish/emit; no event context leaks
    // past the scoped downstream call.
    assert.equal(res.end('ok'), 'ended');
    assert.equal(eventAtResponseEnd, undefined);
    assert.equal(getEvent(), undefined);
  });
});
