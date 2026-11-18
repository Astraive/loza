import { strict as assert } from 'node:assert';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';

const scriptPath = fileURLToPath(new URL('../scripts/check-coverage.ts', import.meta.url));

function runCoverageCheck(lcov: string): { status: number | null; output: string } {
  const dir = mkdtempSync(join(tmpdir(), 'loza-check-coverage-'));
  try {
    const lcovPath = join(dir, 'lcov.info');
    writeFileSync(lcovPath, lcov);
    const result = spawnSync(process.execPath, [scriptPath, lcovPath], { encoding: 'utf8' });
    return { status: result.status, output: `${result.stdout ?? ''}${result.stderr ?? ''}` };
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

test('counts DA lines that carry an lcov checksum', () => {
  const { status, output } = runCoverageCheck([
    'TN:',
    'SF:src/index.ts',
    'DA:1,1,sha1',
    'DA:2,1,sha2',
    'DA:3,1,sha3',
    'DA:4,1',
    'end_of_record',
    '',
  ].join('\n'));
  assert.equal(status, 0, output);
  assert.match(output, /Production line coverage: 4\/4 \(100\.00%\)/);
});

test('uncovered checksum lines count against the 95% gate', () => {
  const { status, output } = runCoverageCheck([
    'TN:',
    'SF:src/index.ts',
    'DA:1,1,sha1',
    'DA:2,0,sha2',
    'DA:3,1,sha3',
    'DA:4,0,sha4',
    'DA:5,1',
    'DA:6,1',
    'LF:6',
    'LH:4',
    'end_of_record',
    '',
  ].join('\n'));
  assert.notEqual(status, 0, 'a 4/6 (66.67%) result must fail the 95% gate');
  assert.match(output, /Production line coverage: 4\/6 \(66\.67%\)/);
  assert.match(output, /below the required 95%/);
});
