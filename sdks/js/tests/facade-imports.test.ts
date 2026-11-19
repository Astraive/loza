import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import * as facade from '../src/loza.ts';

const here = path.dirname(fileURLToPath(import.meta.url));
const facadeSource = fs.readFileSync(path.join(here, '..', 'src', 'loza.ts'), 'utf8');

describe('Loza facade import hygiene', () => {
  it('does not import fromRequest for local use (it is only re-exported)', () => {
    const importStatement = facadeSource.match(
      /^import\s*\{[^}]*\}\s*from\s*['"]\.\/core\/logger\.ts['"]/m,
    );
    assert.ok(importStatement, 'expected a named import from ./core/logger.ts');
    const specifiers = importStatement[0]
      .replace(/^import\s*\{/, '')
      .replace(/\}\s*from.*$/, '')
      .split(',')
      .map((part) => part.trim().split(/\s+as\s+/)[0].trim())
      .filter(Boolean);
    assert.ok(
      !specifiers.includes('fromRequest'),
      'fromRequest must not be imported into loza.ts; it is re-exported from its source instead',
    );
  });

  it('still re-exports fromRequest so the public API is preserved', () => {
    assert.match(
      facadeSource,
      /export\s*\{\s*fromRequest\s*\}\s*from\s*['"]\.\/core\/logger\.ts['"]/,
    );
    assert.equal(typeof facade.fromRequest, 'function');
  });
});
