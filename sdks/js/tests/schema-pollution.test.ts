import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { Event } from '../src/core/event.ts';
import { EventView } from '../src/core/event-view.ts';
import { DefaultSchema } from '../src/core/schema.ts';

function encodeAttrs(attrs: Record<string, any>): Record<string, any> {
  const event = new Event({ event: 'probe', service: 'svc' }, 'svc', 'test');
  event.attrs = attrs;
  return new DefaultSchema().encode(new EventView(event));
}

describe('Schema prototype-pollution hardening', () => {
  it('does not pollute Object.prototype via a __proto__ dot-path attr key', () => {
    const marker = '__proto___pollution_marker_0554';
    delete (Object.prototype as any)[marker];
    try {
      const encoded = encodeAttrs({ [`__proto__.${marker}`]: 'PWNED' });
      assert.equal(({} as any)[marker], undefined, 'Object.prototype must not be polluted');
      assert.equal(Object.hasOwn(Object.prototype, marker), false);
      assert.equal(encoded.attrs['loza___proto__'][marker], 'PWNED', 'unsafe segment is stored under a safe key');
    } finally {
      delete (Object.prototype as any)[marker];
    }
  });

  it('does not pollute via constructor/prototype segments or a bare __proto__ key', () => {
    const marker = '__proto___ctor_marker_0554';
    delete (Object.prototype as any)[marker];
    delete (Object as any)[marker];
    try {
      const encoded = encodeAttrs({
        [`constructor.prototype.${marker}`]: 'PWNED',
        [`constructor.${marker}`]: 'PWNED',
        ['__proto__']: { [marker]: 'PWNED' },
      });
      assert.equal(({} as any)[marker], undefined, 'Object.prototype must not be polluted');
      assert.equal((Object as any)[marker], undefined, 'Object constructor must not be polluted');
      assert.equal(encoded.attrs['loza_constructor']['loza_prototype'][marker], 'PWNED');
      assert.equal(encoded.attrs['loza_constructor'][marker], 'PWNED');
      assert.equal(encoded.attrs['loza___proto__'][marker], 'PWNED');
    } finally {
      delete (Object.prototype as any)[marker];
      delete (Object as any)[marker];
    }
  });

  it('still expands benign dotted keys and rejects unsafe segments inside them', () => {
    const encoded = encodeAttrs({
      'custom.deep.value': 42,
      'safe.__proto__.nested': 'PWNED',
      'constructor.safe.leaf': 'kept?',
    });
    assert.deepEqual(encoded.attrs.custom, { deep: { value: 42 } });
    assert.equal(encoded.attrs.safe['loza___proto__'].nested, 'PWNED');
    assert.equal(encoded.attrs['loza_constructor'].safe.leaf, 'kept?');
    assert.equal(({} as any).nested, undefined);
  });
});
