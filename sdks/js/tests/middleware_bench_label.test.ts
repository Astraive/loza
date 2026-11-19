import { it } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const benchPath = resolve(__dirname, '..', 'bench', 'middleware.bench.ts');

it('startEvent-only middleware bench measures only startEvent', () => {
  const source = readFileSync(benchPath, 'utf8');
  const match = source.match(
    /bench\(\s*['"]startEvent only \(no middleware overhead\)['"]\s*,\s*\(\)\s*=>\s*\{([\s\S]*?)\n\s*\}\s*\);/,
  );
  assert.ok(match, 'expected a bench labelled "startEvent only (no middleware overhead)"');

  const body = match![1];
  assert.match(body, /logger\.startEvent\(/, 'callback must start an event');
  assert.doesNotMatch(body, /\.finish\(/, 'callback must not finish the event');
  assert.doesNotMatch(body, /\.emit\(/, 'callback must not emit the event');
});
