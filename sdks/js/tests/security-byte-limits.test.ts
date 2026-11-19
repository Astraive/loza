import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { SecurityLimiter } from '../src/config/security.ts';

describe('SecurityLimiter UTF-8 byte limits', () => {
  it('measures maxFieldBytes in UTF-8 bytes, not UTF-16 code units', () => {
    const limiter = new SecurityLimiter({ maxFieldBytes: 3 });

    // ASCII: code-unit length and byte length agree (unchanged behavior).
    assert.equal(limiter.isFieldOversized('123'), false);
    assert.equal(limiter.isFieldOversized('1234'), true);

    // 3-byte BMP chars: 2 code units but 6 UTF-8 bytes — over the 3-byte cap.
    assert.equal(limiter.isFieldOversized('中'), false); // exactly 3 bytes: at the cap
    assert.equal(limiter.isFieldOversized('中中'), true); // 2 units, 6 bytes

    // 4-byte astral chars (emoji): 2 code units (surrogate pair) but 4 bytes.
    assert.equal(limiter.isFieldOversized('😀'), true);
  });

  it('measures maxEventBytes in UTF-8 bytes when deciding to drop', () => {
    const limiter = new SecurityLimiter({ maxEventBytes: 3 });

    assert.equal(limiter.shouldDrop('123', 0), false);
    assert.equal(limiter.shouldDrop('1234', 0), true);

    // 6 bytes but only 2 UTF-16 code units: must be dropped.
    assert.equal(limiter.shouldDrop('中中', 0), true);

    // A 4-byte emoji is a 2-code-unit surrogate pair: must be dropped.
    assert.equal(limiter.shouldDrop('😀', 0), true);

    // attrCount is still evaluated independently of byte size.
    assert.equal(limiter.shouldDrop('1', 1), false);
  });

  it('enforces the default maxEventBytes cap against multibyte payloads', () => {
    const limiter = new SecurityLimiter(); // maxEventBytes = 262144
    // 100_000 code units but 300_000 UTF-8 bytes: over the default cap.
    assert.equal(limiter.shouldDrop('中'.repeat(100_000), 0), true);
    // Well under the cap even in bytes: must not be dropped.
    assert.equal(limiter.shouldDrop('中'.repeat(10), 0), false);
  });
});
