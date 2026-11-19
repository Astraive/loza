import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { uuidv7 } from '../src/index.ts';
import { resetUUIDGenerator, setUUIDGenerator } from '../src/core/uuidv7.ts';

const UUIDV7_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

/** Run fn with a controllable Date.now that is always restored. */
function withClock<T>(now: () => number, fn: () => T): T {
  const originalNow = Date.now;
  Date.now = now;
  try {
    return fn();
  } finally {
    Date.now = originalNow;
    resetUUIDGenerator();
  }
}

describe('uuidv7 monotonicity', () => {
  it('produces strictly increasing IDs within the same millisecond', () => {
    const fixed = 1_800_000_000_000;
    withClock(() => fixed, () => {
      resetUUIDGenerator();
      const ids = Array.from({ length: 200 }, () => uuidv7());
      for (const id of ids) {
        assert.match(id, UUIDV7_RE);
      }
      for (let i = 1; i < ids.length; i++) {
        assert.ok(ids[i] > ids[i - 1], `ID ${i} (${ids[i]}) must sort after ${ids[i - 1]}`);
      }
      assert.deepEqual(ids, [...ids].sort());
    });
  });

  it('stays monotonic when the system clock rolls backwards', () => {
    let now = 1_800_000_000_000;
    withClock(() => now, () => {
      resetUUIDGenerator();
      const before = Array.from({ length: 10 }, () => uuidv7());
      now -= 5_000; // clock jump backwards
      const after = Array.from({ length: 10 }, () => uuidv7());
      const ids = [...before, ...after];
      for (let i = 1; i < ids.length; i++) {
        assert.ok(ids[i] > ids[i - 1], `ID ${i} must remain monotonic across clock rollback`);
      }
      // Timestamp is frozen while the clock is behind the last seen time.
      const ts = (id: string) => id.replace(/-/g, '').slice(0, 12);
      assert.equal(ts(after[0]), ts(before[before.length - 1]));
    });
  });

  it('keeps the custom ID generator override intact', () => {
    setUUIDGenerator(() => 'custom-id');
    try {
      assert.equal(uuidv7(), 'custom-id');
    } finally {
      resetUUIDGenerator();
    }
  });
});
