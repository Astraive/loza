import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { Event, HashString, type Attr } from '../src/core/event.ts';
import { sanitizeEvent } from '../src/core/sanitize.ts';

function expected(value: unknown): string {
  return createHash('sha256').update(String(value)).digest('hex');
}

function hashAttr(key: string, value: unknown): Attr {
  return { key, kind: 'any', value, hashValue: true };
}

describe('sanitizeEvent hash-key value coercion', () => {
  it('hashes string values as before', () => {
    const event = new Event({ event: 'sanitize' }, 'svc', 'test');
    event.enrich(HashString('email', 'a@example.com'));
    const sanitized = sanitizeEvent(event);
    assert.equal(sanitized.attrs.email, expected('a@example.com'));
    assert.match(sanitized.attrs.email, /^[a-f0-9]{64}$/);
  });

  it('hashes non-string values instead of emitting them in cleartext', () => {
    const event = new Event({ event: 'sanitize' }, 'svc', 'test');
    event.enrich(
      hashAttr('number', 123),
      hashAttr('bigint', 9007199254740993n),
      hashAttr('boolean', true),
      hashAttr('object', { card: '4111111111111111' }),
    );
    const sanitized = sanitizeEvent(event);

    for (const [key, value] of Object.entries({
      number: 123,
      bigint: 9007199254740993n,
      boolean: true,
      object: { card: '4111111111111111' },
    })) {
      assert.equal(sanitized.attrs[key], expected(value), `${key} should be hashed`);
      assert.match(sanitized.attrs[key], /^[a-f0-9]{64}$/, `${key} should be a sha256 hex digest`);
    }

    // No cleartext sensitive values leak into any hashed attribute.
    assert.ok(!JSON.stringify(sanitized.attrs).includes('4111111111111111'));
  });

  it('does not mutate the original event', () => {
    const event = new Event({ event: 'sanitize' }, 'svc', 'test');
    event.enrich(hashAttr('number', 123));
    sanitizeEvent(event);
    assert.equal(event.attrs.number, 123);
  });
});
