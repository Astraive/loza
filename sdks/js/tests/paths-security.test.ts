import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { setPath, getPath } from '../src/core/paths.ts';

describe('setPath prototype-pollution hardening', () => {
  it('does not pollute Object.prototype via a __proto__ path', () => {
    const root: Record<string, any> = {};
    setPath(root, '__proto__.polluted', true);
    assert.equal(({} as any).polluted, undefined);
    assert.equal((Object.prototype as any).polluted, undefined);
    assert.equal(Object.prototype.hasOwnProperty.call(root, '__proto__'), false);
    assert.equal(Object.getPrototypeOf(root), Object.prototype);
  });

  it('rejects constructor/prototype traversal paths', () => {
    const root: Record<string, any> = {};
    setPath(root, 'constructor.prototype.polluted', true);
    setPath(root, 'a.constructor.prototype.polluted', true);
    setPath(root, 'prototype.polluted', true);
    assert.equal(({} as any).polluted, undefined);
    assert.equal((Object.prototype as any).polluted, undefined);
  });

  it('rejects nested __proto__ segments and direct __proto__ writes', () => {
    const root: Record<string, any> = { a: {} };
    const before = Object.getPrototypeOf(root.a);
    setPath(root, 'a.__proto__.polluted', true);
    setPath(root, 'a.__proto__', { polluted: true });
    assert.equal(({} as any).polluted, undefined);
    assert.equal(Object.getPrototypeOf(root.a), before);
  });

  it('uses own properties rather than inherited values for intermediate segments', () => {
    const root: Record<string, any> = {};
    setPath(root, 'toString.value', 42);
    assert.equal(Object.prototype.hasOwnProperty.call(root, 'toString'), true);
    assert.equal(getPath(root, 'toString.value'), 42);
  });

  it('preserves normal nested set/get behavior', () => {
    const root: Record<string, any> = {};
    setPath(root, 'a.b.c', 7);
    assert.equal(getPath(root, 'a.b.c'), 7);
  });
});
