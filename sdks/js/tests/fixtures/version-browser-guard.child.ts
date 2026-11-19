/**
 * Child-process fixture for version-browser-guard.test.ts.
 *
 * Runs in an isolated `bun test` process so the `node:fs` mock is applied
 * before anything else loads the version module. This file is intentionally
 * not named `*.test.ts` so the main suite does not pick it up directly.
 */
import { expect, mock, test } from 'bun:test';

test('version module loads when node:fs is unavailable', async () => {
  // Emulate a browser/non-Node runtime in which the `node:fs` specifier is
  // not resolvable. A static import throws while the module is being linked,
  // before any try/catch in the module body can run.
  mock.module('node:fs', () => ({
    get existsSync() {
      throw new Error('node:fs is unavailable in this environment');
    },
    readFileSync() {
      throw new Error('node:fs is unavailable in this environment');
    },
  }));

  const { SDK_VERSION } = await import('../../src/config/version.ts');
  expect(SDK_VERSION).toBe('0.4.1');
});
