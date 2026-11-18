import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { runInNewContext } from 'node:vm';
import { redact, hashKeys, maskKeys, redactPatterns } from '../src/redaction/redactor.ts';

describe('Redaction boundaries', () => {
  it('supports alias redaction and recursive arrays', () => {
    const result = redact('secret', 'password')({
      secret: 'top-secret',
      nested: { user_password: 'hidden', visible: 'ok' },
      values: [{ token: 'one' }, 'plain'],
    });
    assert.deepEqual(result, {
      secret: '[REDACTED]',
      nested: { user_password: '[REDACTED]', visible: 'ok' },
      values: [{ token: 'one' }, 'plain'],
    });
  });

  it('hashes matching values while preserving nonmatching values', () => {
    const result = hashKeys('email')({ email: 'a@example.com', nested: { email: 'b@example.com' }, count: 2 });
    const resultEmail = result.email;
    if (typeof resultEmail !== 'string') throw new Error('expected hashed email');
    assert.match(resultEmail, /^sha256:[a-f0-9]{64}$/);
    const nested = result.nested;
    if (!nested || typeof nested !== 'object' || !('email' in nested) || typeof nested.email !== 'string') {
      throw new Error('expected hashed nested email');
    }
    assert.match(nested.email, /^sha256:[a-f0-9]{64}$/);
    assert.equal(result.count, 2);
  });

  it('masks short and long strings but leaves non-strings unchanged', () => {
    const result = maskKeys('card')({ card_number: '12345678', card_code: '1234', card_count: 5, name: 'Jane' });
    assert.deepEqual(result, {
      card_number: '12****78',
      card_code: '****',
      card_count: 5,
      name: 'Jane',
    });
  });

  it('redacts keys selected by case-insensitive patterns', () => {
    const result = redactPatterns('card')({ card_number: '12345678', name: 'Jane' });
    assert.deepEqual(result, { card_number: '[REDACTED]', name: 'Jane' });
  });

  it('redacts sensitive keys nested inside class instances', () => {
    class Credentials {
      password = 'hunter2';
      label = 'primary';
    }
    const result = redact('password')({ credentials: new Credentials() });
    assert.deepEqual(result.credentials, { password: '[REDACTED]', label: 'primary' });
  });

  it('redacts sensitive keys inside null-prototype objects', () => {
    const inner = Object.create(null);
    inner.token = 'abc';
    inner.visible = 'ok';
    const result = redact('token')({ inner });
    assert.equal(result.inner.token, '[REDACTED]');
    assert.equal(result.inner.visible, 'ok');
  });

  it('redacts sensitive keys inside objects from another realm', () => {
    const foreign = runInNewContext('({ secret: "s3cr3t", keep: "yes" })');
    const result = redact('secret')({ foreign });
    assert.deepEqual(result.foreign, { secret: '[REDACTED]', keep: 'yes' });
  });

  it('preserves opaque built-ins instead of flattening them', () => {
    const when = new Date('2020-01-01T00:00:00.000Z');
    const result = redact('password')({ when, password: 'p' });
    assert.equal(result.when, when);
    assert.equal(result.password, '[REDACTED]');
  });
});
