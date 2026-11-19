import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  defaultRedactor, redactKeys, dropKeys, maskKeys, composeRedactors,
} from '../src/redaction/redactor.ts';

describe('Redactor', () => {
  it('defaultRedactor redacts sensitive keys', () => {
    const r = defaultRedactor();
    const payload = {
      service: 'checkout',
      attrs: { password: 'secret', api_key: 'sk-123', user_id: 'u1' },
    };
    const result = r(payload);
    assert.equal(result.attrs.password, '[REDACTED]');
    assert.equal(result.attrs.api_key, '[REDACTED]');
    assert.equal(result.attrs.user_id, 'u1');
  });

  it('redactKeys redacts specified keys', () => {
    const r = redactKeys('token', 'secret');
    const result = r({ attrs: { token: 'abc', secret: 'xyz', name: 'test' } });
    assert.equal(result.attrs.token, '[REDACTED]');
    assert.equal(result.attrs.secret, '[REDACTED]');
    assert.equal(result.attrs.name, 'test');
  });

  it('dropKeys removes matching keys', () => {
    const r = dropKeys('password', 'secret');
    const result = r({ attrs: { password: 'abc', secret: 'xyz', name: 'test' } });
    assert.equal(result.attrs.password, undefined);
    assert.equal(result.attrs.secret, undefined);
    assert.equal(result.attrs.name, 'test');
  });

  it('maskKeys partially masks values', () => {
    const r = maskKeys('credit_card');
    const result = r({ attrs: { credit_card: '4111111111111111' } });
    assert.ok(result.attrs.credit_card.includes('*'));
    assert.ok(result.attrs.credit_card.startsWith('41'));
    assert.ok(result.attrs.credit_card.endsWith('11'));
  });

  it('maskKeys masks non-string sensitive values instead of exposing them', () => {
    const r = maskKeys('pin', 'secret', 'flag', 'payload', 'codes');
    const result = r({
      pin: 1234,
      secret: 99,
      flag: true,
      payload: { nested: 'value' },
      codes: [10, 20, 30],
      name: 'Jane',
    });
    assert.notEqual(result.pin, 1234);
    assert.notEqual(result.secret, 99);
    assert.notEqual(result.flag, true);
    assert.notDeepEqual(result.payload, { nested: 'value' });
    assert.notDeepEqual(result.codes, [10, 20, 30]);
    // Every masked value must be a string containing a mask character.
    for (const key of ['pin', 'secret', 'flag', 'payload', 'codes']) {
      assert.equal(typeof result[key], 'string', `${key} should be a masked string`);
      assert.ok(String(result[key]).includes('*'), `${key} should be masked`);
    }
    // Non-matching values remain untouched.
    assert.equal(result.name, 'Jane');
  });

  it('composeRedactors chains redactors', () => {
    const r = composeRedactors(
      redactKeys('password'),
      dropKeys('secret'),
    );
    const result = r({ attrs: { password: 'abc', secret: 'xyz', name: 'test' } });
    assert.equal(result.attrs.password, '[REDACTED]');
    assert.equal(result.attrs.secret, undefined);
    assert.equal(result.attrs.name, 'test');
  });

  it('default redactor uses 14 safety-net keys', () => {
    const r = defaultRedactor();
    const sensitiveKeys = [
      'password', 'passwd', 'pwd', 'secret', 'token', 'access_token',
      'refresh_token', 'api_key', 'apikey', 'auth', 'authorization',
      'credential', 'private_key', 'client_secret',
    ];
    for (const key of sensitiveKeys) {
      const result = r({ attrs: { [key]: 'value' } });
      assert.equal(result.attrs[key], '[REDACTED]', `expected ${key} to be redacted`);
    }
  });
});
