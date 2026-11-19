import { describe, it, expect } from 'bun:test';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

describe('version loading in browser / non-Node environments', () => {
  it('falls back instead of throwing when node:fs cannot be resolved', () => {
    // The fixture runs in its own process with `node:fs` mocked as unavailable.
    // With the old static `import { ... } from 'node:fs'` the import failed at
    // module evaluation, before loadVersion's try/catch could run, so simply
    // importing the module threw.
    const fixture = fileURLToPath(
      new URL('./fixtures/version-browser-guard.child.ts', import.meta.url),
    );
    const result = spawnSync(process.execPath, ['test', fixture], {
      cwd: fileURLToPath(new URL('..', import.meta.url)),
      encoding: 'utf-8',
    });

    if (result.status !== 0) {
      throw new Error(result.stdout + result.stderr);
    }
    expect(result.status).toBe(0);
  });

  it('still reads the real version on the normal Node path', async () => {
    const { SDK_VERSION } = await import('../src/config/version.ts?normal-node-path');
    expect(typeof SDK_VERSION).toBe('string');
    expect(SDK_VERSION).toBe('0.4.1');
  });
});
