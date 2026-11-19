import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { Event, String, Int, setClock } from '../src/core/event.ts';
import {
  ProcessHandle, TimerHandle, GroupHandle, withProcess, withGroup, withTimer,
  measure, phase, span, step, stopwatch,
} from '../src/core/timing.ts';

function withClock(run: (setNow: (value: number) => void) => void): void {
  const originalNow = Date.now;
  let now = 1000;
  const previousClock = setClock(() => now);
  Date.now = () => now;
  try {
    run(value => { now = value; });
  } finally {
    Date.now = originalNow;
    setClock(previousClock);
  }
}

describe('Timing boundaries', () => {
  it('records process, group, and timer entries with attrs and status codes', () => {
    withClock(setNow => {
      const event = new Event({ event: 'timed', service: 'svc' }, 'svc', 'test');
      setNow(1010);
      const process = event.startProcess('step');
      assert.equal(process instanceof ProcessHandle, true);
      setNow(1030);
      process.finish(String('detail', 'value'), Int('status_code', 201));
      const group = event.startGroup('phase');
      assert.equal(group instanceof GroupHandle, true);
      setNow(1050);
      group.finish(String('detail', 'group'), Int('status_code', 202));
      const timer = event.startTimer('span');
      assert.equal(timer instanceof TimerHandle, true);
      setNow(1080);
      timer.stop(String('detail', 'timer'), Int('status_code', 203));
      assert.equal(process.duration(), 70);
      assert.equal(group.duration(), 50);
      assert.equal(timer.duration(), 30);
      assert.equal(event.processes[0].status_code, 201);
      assert.equal(event.processes[0].attrs?.detail, 'value');
      assert.equal(event.groups[0].status_code, 202);
      assert.equal(event.timers[0].status_code, 203);
      timer.stopTimer();
      assert.equal(event.timers.length, 2);
    });
  });

  it('records attrs passed to withProcess/withGroup/withTimer and phase/span/step', () => {
    withClock(() => {
      const event = new Event({ event: 'timed', service: 'svc' }, 'svc', 'test');
      withProcess(event, 'p', () => {}, String('key', 'a'), Int('value', 1));
      withGroup(event, 'g', () => {}, String('key', 'a'));
      withTimer(event, 't', () => {}, String('key', 'a'));
      phase(event, 'ph', String('key', 'a')).finish();
      span(event, 'sp', String('key', 'a')).stop();
      step(event, 'st', String('key', 'a')).finish();
      assert.deepEqual(event.processes[0].attrs, { key: 'a', value: 1 });
      assert.deepEqual(event.groups[0].attrs, { key: 'a' });
      assert.deepEqual(event.timers[0].attrs, { key: 'a' });
      assert.deepEqual(event.groups[1].attrs, { key: 'a' });
      assert.deepEqual(event.timers[1].attrs, { key: 'a' });
      assert.deepEqual(event.processes[1].attrs, { key: 'a' });
    });
  });

  it('applies creation-time status codes and merges with finish-time attrs', () => {
    withClock(() => {
      const event = new Event({ event: 'timed', service: 'svc' }, 'svc', 'test');
      event.startProcess('p', String('created', 'yes'), Int('status_code', 201))
        .finish(String('finished', 'yes'));
      assert.equal(event.processes[0].status_code, 201);
      assert.deepEqual(event.processes[0].attrs, { created: 'yes', finished: 'yes' });
    });
  });

  it('records errors and supports timing aliases and standalone stopwatches', () => {
    withClock(setNow => {
      const event = new Event({ event: 'timed', service: 'svc' }, 'svc', 'test');
      setNow(1010);
      step(event, 'step').finishError('failed');
      phase(event, 'phase').finishGroupError(new Error('group failed'));
      span(event, 'span').stop();
      withProcess(event, 'wrapped', () => {}, String('k', 'v'));
      withGroup(event, 'wrapped-group', () => {});
      withTimer(event, 'wrapped-timer', () => {});
      assert.equal(event.processes.length, 2);
      assert.equal(event.groups.length, 2);
      assert.equal(event.timers.length, 2);
      assert.throws(() => withProcess(event, 'bad', () => { throw new Error('process'); }), /process/);
      assert.throws(() => withGroup(event, 'bad-group', () => { throw new Error('group'); }), /group/);
      assert.throws(() => withTimer(event, 'bad-timer', () => { throw new Error('timer'); }), /timer/);

      setNow(1050);
      const measured = measure();
      const stopped = stopwatch();
      setNow(1060);
      assert.equal(measured.elapsed(), 10);
      assert.equal(stopped.elapsed(), 10);
    });
  });
  it('records a timer entry when the withTimer wrapped function throws', () => {
    withClock(setNow => {
      const event = new Event({ event: 'timed', service: 'svc' }, 'svc', 'test');
      setNow(1010);
      assert.throws(
        () => withTimer(event, 'failing', () => { setNow(1025); throw new Error('timer exploded'); }),
        /timer exploded/,
      );
      assert.equal(event.timers.length, 1);
      assert.equal(event.timers[0].name, 'failing');
      assert.equal(event.timers[0].duration_ms, 15);
      assert.equal(event.timers[0].attrs?.error_message, 'Error: timer exploded');
    });
  });

  it('handles timing adapters with empty event state and omitted attrs', () => {
    withClock(setNow => {
      const processEvent: Record<string, any> = {};
      const process = new ProcessHandle(processEvent, 'empty-process', 1, 1000);
      setNow(1010);
      process.finish(null, { key: 'status_code', value: 0 });
      assert.equal(processEvent.processes.length, 1);

      const groupEvent: Record<string, any> = {};
      const group = new GroupHandle(groupEvent, 'empty-group', 1000);
      setNow(1020);
      group.finish(null, { key: 'status_code', value: 0 });
      assert.equal(groupEvent.groups.length, 1);

      const timerEvent: Record<string, any> = {};
      const timer = new TimerHandle(timerEvent, 'empty-timer', 1000);
      setNow(1030);
      timer.stop(null, { key: 'status_code', value: 0 });
      assert.equal(timerEvent.timers.length, 1);
    });
  });

  // The shared extractStatusCode helper is the single source of truth for
  // status-code parsing; all three handles must observe identical behaviour.
  it('extracts numeric status codes and keeps other attrs consistent across handles', () => {
    withClock(setNow => {
      const event = new Event({ event: 'timed', service: 'svc' }, 'svc', 'test');
      setNow(1010);
      const process = event.startProcess('step');
      const group = event.startGroup('phase');
      const timer = event.startTimer('span');
      setNow(1020);

      // A numeric status code is lifted out of attrs; a non-numeric one stays an attr.
      process.finish(Int('status_code', 201), String('detail', 'value'), String('status_code', 'oops'));
      group.finish(Int('status_code', 202), String('detail', 'value'), String('status_code', 'oops'));
      timer.stop(Int('status_code', 203), String('detail', 'value'), String('status_code', 'oops'));

      for (const [entry, code] of [
        [event.processes[0], 201],
        [event.groups[0], 202],
        [event.timers[0], 203],
      ] as const) {
        assert.equal(entry.status_code, code);
        assert.equal(entry.attrs?.detail, 'value');
        // The last non-numeric status_code overwrites the regular attr of the same key.
        assert.equal(entry.attrs?.status_code, 'oops');
      }
    });
  });

  it('omits status_code consistently when it is absent, zero, or non-numeric', () => {
    withClock(setNow => {
      const event = new Event({ event: 'timed', service: 'svc' }, 'svc', 'test');
      setNow(1010);
      const process = event.startProcess('step');
      const group = event.startGroup('phase');
      const timer = event.startTimer('span');
      setNow(1020);

      // Zero is treated as "no status code"; a string status_code is kept as an attr.
      process.finish(Int('status_code', 0));
      group.finish(String('status_code', '500'));
      timer.stop();

      assert.equal('status_code' in event.processes[0], false);
      assert.equal('status_code' in event.groups[0], false);
      assert.equal(event.groups[0].attrs?.status_code, '500');
      assert.equal('status_code' in event.timers[0], false);
    });
  });
});
