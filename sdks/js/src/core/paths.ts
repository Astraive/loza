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
