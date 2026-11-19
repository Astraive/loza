/** Segments that can reach or mutate an object's prototype chain. */
const UNSAFE_PATH_SEGMENTS = new Set(['__proto__', 'prototype', 'constructor']);

/** Set a value at a dot-separated path in an object. */
export function setPath(root: Record<string, any>, key: string, value: any): void {
  const parts = key.split('.');
  // Untrusted paths must never descend through (or write to) prototype accessors.
  if (parts.some((part) => UNSAFE_PATH_SEGMENTS.has(part))) return;
  let cur = root;
  for (let i = 0; i < parts.length - 1; i++) {
    const part = parts[i];
    // Only own, plain-object children are valid containers. Inherited values
    // (e.g. Object.prototype accessors) must never be treated as objects here.
    if (
      !Object.prototype.hasOwnProperty.call(cur, part) ||
      typeof cur[part] !== 'object' ||
      cur[part] === null ||
      Array.isArray(cur[part])
    ) {
      cur[part] = Object.create(null);
    }
    cur = cur[part];
  }
  cur[parts[parts.length - 1]] = value;
import { setSafePath } from './prototype-safe.ts';

/** Set a value at a dot-separated path in an object (prototype-pollution safe). */
export function setPath(root: Record<string, any>, key: string, value: any): void {
  setSafePath(root, key, value);
}

/** Get a value at a dot-separated path in an object. */
export function getPath(root: Record<string, any>, key: string): unknown {
  const parts = key.split('.');
  let cur: any = root;
  for (const part of parts) {
    if (cur == null || typeof cur !== 'object') return undefined;
    cur = cur[part];
  }
  return cur;
}

/** Delete a value at a dot-separated path in an object. */
export function deletePath(root: Record<string, any>, key: string): void {
  const parts = key.split('.');
  let cur: any = root;
  for (let i = 0; i < parts.length - 1; i++) {
    if (cur == null || typeof cur !== 'object') return;
    cur = cur[parts[i]];
  }
  if (cur != null && typeof cur === 'object') {
    delete cur[parts[parts.length - 1]];
  }
}
