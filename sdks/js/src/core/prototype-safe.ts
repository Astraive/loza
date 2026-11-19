/**
 * Prototype-pollution hardening for dot-separated path assignment.
 *
 * Event attribute keys can be attacker-controlled (untrusted log payloads,
 * ingestion bridges, user metadata). A naive `obj[key] = value` walker lets a
 * key such as `__proto__.polluted` or `constructor.prototype.polluted` reach
 * `Object.prototype` and corrupt every object in the runtime.
 *
 * Defenses applied here (defense in depth):
 *   1. The dangerous segments `__proto__`, `constructor`, and `prototype` are
 *      never used as raw keys. They are stored under a namespaced key instead
 *      (see {@link sanitizeSegment}), so the attribute value is preserved and
 *      the event is not silently dropped.
 *   2. Ownership is checked with `hasOwnProperty.call` rather than `in`, so an
 *      inherited property can never be followed during traversal.
 *   3. Every write uses `Object.defineProperty` (with `enumerable: true`), so
 *      even a `__proto__` key would define a plain own property instead of
 *      invoking the inherited `__proto__` setter.
 */

/** Segments that must never be used as raw object keys. */
export const UNSAFE_SEGMENTS: ReadonlySet<string> = new Set([
  '__proto__',
  'constructor',
  'prototype',
]);

/** Namespace prefix used to store a sanitized form of an unsafe segment. */
const SANITIZED_PREFIX = 'loza_';

/**
 * Return a safe key for a path segment. Unsafe segments are preserved under a
 * `loza_`-prefixed key (e.g. `__proto__` -> `loza___proto__`) so the value is
 * still emitted instead of being silently dropped.
 */
export function sanitizeSegment(segment: string): string {
  return UNSAFE_SEGMENTS.has(segment) ? `${SANITIZED_PREFIX}${segment}` : segment;
}

/** Assign `value` at a dot-separated `path` on `root` without reaching any built-in prototype. */
export function setSafePath(root: Record<string, any>, path: string, value: any): void {
  const parts = path.split('.');
  let current = root;
  for (let i = 0; i < parts.length - 1; i++) {
    const key = sanitizeSegment(parts[i]);
    if (
      !Object.prototype.hasOwnProperty.call(current, key) ||
      typeof current[key] !== 'object' ||
      current[key] === null ||
      Array.isArray(current[key])
    ) {
      Object.defineProperty(current, key, {
        value: {},
        writable: true,
        enumerable: true,
        configurable: true,
      });
    }
    current = current[key];
  }
  Object.defineProperty(current, sanitizeSegment(parts[parts.length - 1]), {
    value,
    writable: true,
    enumerable: true,
    configurable: true,
  });
}
