import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { RotatingFileSink } from '../src/sinks/standard-sinks.ts';

describe('RotatingFileSink backup collision', () => {
  it('keeps distinct backups when rotations happen within the same second', () => {
    const realToISOString = Date.prototype.toISOString;
    // Freeze the wall clock so both rotations produce the same timestamp.
    // This is the exact condition that previously caused the second rename
    // to overwrite the first backup and silently lose its contents.
    Date.prototype.toISOString = () => '2026-01-02T03:04:05.678Z';

    const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'loza-rotate-'));
    try {
      const file = path.join(tempDir, 'events.ndjson');
      fs.writeFileSync(file, 'ORIGINAL\n');

      const sink = new RotatingFileSink(file, 1, 10);
      // maxBytes=1 means each write forces a rotation before appending.
      sink.write('FIRST');
      sink.write('SECOND');

      const backups = fs.readdirSync(tempDir)
        .filter(name => name.startsWith('events.ndjson.'))
        .map(name => ({
          name,
          content: fs.readFileSync(path.join(tempDir, name), 'utf8'),
        }));

      // Two rotations must leave two distinct backups, preserving the data
      // present before each one. The old second-truncated naming overwrote
      // the first backup here, losing "ORIGINAL".
      assert.equal(backups.length, 2, `expected two distinct backups, got ${backups.length}`);
      const names = backups.map(b => b.name);
      assert.equal(new Set(names).size, 2, `expected unique backup names, got ${names}`);
      const contents = backups.map(b => b.content);
      assert.ok(contents.some(c => c.includes('ORIGINAL')), 'content from the first rotation was lost');
      assert.ok(contents.some(c => c.includes('FIRST')), 'content from the second rotation was lost');
      assert.equal(fs.readFileSync(file, 'utf8'), 'SECOND\n');
    } finally {
      Date.prototype.toISOString = realToISOString;
      fs.rmSync(tempDir, { recursive: true, force: true });
    }
  });

  it('never overwrites an existing backup destination', () => {
    const realToISOString = Date.prototype.toISOString;
    Date.prototype.toISOString = () => '2026-01-02T03:04:05.678Z';

    const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'loza-rotate-preexisting-'));
    try {
      const file = path.join(tempDir, 'events.ndjson');
      // Cover both the old second-truncated name and the new ms-preserving
      // name: neither may be clobbered by a fresh rotation.
      const bySecond = `${file}.2026-01-02T03-04-05`;
      const byMillisecond = `${file}.2026-01-02T03-04-05-678Z`;
      fs.writeFileSync(file, 'CURRENT\n');
      fs.writeFileSync(bySecond, 'PREEXISTING-SECOND\n');
      fs.writeFileSync(byMillisecond, 'PREEXISTING-MILLISECOND\n');

      // Directly trigger a rotation whose computed name already exists.
      (sinkFor(file) as unknown as { rotate(): void }).rotate();

      assert.equal(fs.readFileSync(bySecond, 'utf8'), 'PREEXISTING-SECOND\n',
        'an existing second-named backup must not be overwritten');
      assert.equal(fs.readFileSync(byMillisecond, 'utf8'), 'PREEXISTING-MILLISECOND\n',
        'an existing millisecond-named backup must not be overwritten');
      const backups = fs.readdirSync(tempDir).filter(name => name.startsWith('events.ndjson.'));
      assert.equal(backups.length, 3, 'a new backup should be created under a unique name');
    } finally {
      Date.prototype.toISOString = realToISOString;
      fs.rmSync(tempDir, { recursive: true, force: true });
    }
  });
});

function sinkFor(file: string): RotatingFileSink {
  return new RotatingFileSink(file, 1, 10);
}
