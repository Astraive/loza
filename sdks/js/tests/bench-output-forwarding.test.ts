import { it } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const helperPath = fileURLToPath(new URL('../bench/forward-output.js', import.meta.url));

function runChild(source: string) {
  return spawnSync(
    process.execPath,
    [
      '--input-type=module',
      '-e',
      `import { forwardOutput } from ${JSON.stringify(helperPath)};\n${source}`,
    ],
    { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] },
  );
}

it('flushes full piped stdout/stderr before the process exits', () => {
  // A payload larger than the OS pipe buffer would be dropped by an immediate
  // process.exit() in the child, truncating the tail marker below.
  const child = runChild(
    [
      "const stdoutPayload = 's'.repeat(256 * 1024) + 'STDOUT_END';",
      "const stderrPayload = 'e'.repeat(256 * 1024) + 'STDERR_END';",
      'forwardOutput({ stdout: stdoutPayload, stderr: stderrPayload, exitCode: 0 });',
    ].join('\n'),
  );

  assert.equal(child.status, 0);
  assert.ok(child.stdout.endsWith('STDOUT_END'), 'piped stdout tail was truncated');
  assert.ok(child.stderr.endsWith('STDERR_END'), 'piped stderr tail was truncated');
  assert.equal(child.stdout.length, 256 * 1024 + 'STDOUT_END'.length);
  assert.equal(child.stderr.length, 256 * 1024 + 'STDERR_END'.length);
});

it('sets the forwarded exit code and defaults to 1 when it is missing', () => {
  assert.equal(runChild('forwardOutput({ exitCode: 7 });').status, 7);
  assert.equal(runChild('forwardOutput({});').status, 1);
});
