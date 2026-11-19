import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { Event } from '../src/core/event.ts';
import { EventView } from '../src/core/event-view.ts';
import { DefaultSchema } from '../src/core/schema.ts';
import { setPath } from '../src/core/paths.ts';

const DANGEROUS_PATHS = [
  '__proto__.polluted',
  'constructor.prototype.polluted',
  'prototype.polluted',
];

const BUILT_IN_PROTOTYPES = [
  Object.prototype,
  Function.prototype,
  Array.prototype,
  String.prototype,
  Number.prototype,
];

function encodeAttr(key: string, value: any): Record<string, any> {
  const event = new Event({ event: 'security.test', service: 'svc' }, 'svc', 'test');
  event.attrs = { [key]: value };
  return new DefaultSchema().encode(new EventView(event));
}

describe('prototype pollution hardening', () => {
  it('does not pollute Object.prototype via a __proto__ path', () => {
    encodeAttr('__proto__.polluted', 'YES');
    assert.equal(({} as any).polluted, undefined);
    assert.equal(Object.prototype.hasOwnProperty.call(Object.prototype, 'polluted'), false);
  });

  it('does not pollute Object.prototype or Function.prototype via a constructor.prototype path', () => {
    encodeAttr('constructor.prototype.polluted', 'YES');
    assert.equal(({} as any).polluted, undefined);
    assert.equal((Function.prototype as any).polluted, undefined);
    assert.equal(Object.prototype.hasOwnProperty.call(Object.prototype, 'polluted'), false);
  });

  it('does not pollute built-in prototypes via a prototype path', () => {
    encodeAttr('prototype.polluted', 'YES');
    assert.equal(({} as any).polluted, undefined);
    assert.equal(Object.prototype.hasOwnProperty.call(Object.prototype, 'polluted'), false);
  });

  it('leaves every built-in prototype untouched even after all crafted paths', () => {
    const before = BUILT_IN_PROTOTYPES.map(p => Reflect.ownKeys(p).length);
    for (const path of DANGEROUS_PATHS) {
      encodeAttr(path, 'YES');
    }
    const after = BUILT_IN_PROTOTYPES.map(p => Reflect.ownKeys(p).length);
    assert.deepEqual(after, before);
  });

  it('stores crafted keys under sanitized keys instead of silently dropping them', () => {
    const proto = encodeAttr('__proto__.polluted', 'YES');
    assert.equal(proto.attrs['loza___proto__'].polluted, 'YES');

    const ctor = encodeAttr('constructor.prototype.polluted', 'YES');
    assert.equal(ctor.attrs['loza_constructor']['loza_prototype'].polluted, 'YES');

    const prototype = encodeAttr('prototype.polluted', 'YES');
    assert.equal(prototype.attrs['loza_prototype'].polluted, 'YES');
  });

  it('treats a bare __proto__ / constructor / prototype key as an ordinary own property', () => {
    for (const key of ['__proto__', 'constructor', 'prototype']) {
      const encoded = encodeAttr(key, 'YES');
      assert.equal(({} as any).polluted, undefined);
      assert.equal(encoded.attrs[`loza_${key}`], 'YES');
    }
  });

  it('hardens setPath in core/paths.ts against the same crafted paths', () => {
    const before = BUILT_IN_PROTOTYPES.map(p => Reflect.ownKeys(p).length);
    for (const path of DANGEROUS_PATHS) {
      const root: Record<string, any> = {};
      setPath(root, path, 'YES');
      assert.equal(({} as any).polluted, undefined);
      assert.equal(Object.prototype.hasOwnProperty.call(Object.prototype, 'polluted'), false);
    }
    assert.deepEqual(BUILT_IN_PROTOTYPES.map(p => Reflect.ownKeys(p).length), before);
  });

  it('still encodes legitimate nested attribute keys normally', () => {
    const encoded = encodeAttr('custom.deep.value', 42);
    assert.deepEqual(encoded.attrs, { custom: { deep: { value: 42 } } });
  });
});
