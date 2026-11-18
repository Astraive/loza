import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  createLoza, production, noopSink, memorySink, sampleAll,
} from '../src';

const here = path.dirname(fileURLToPath(import.meta.url));
const benchSource = fs.readFileSync(
  path.resolve(here, '..', 'bench', 'sampler.bench.ts'),
  'utf8'
);

describe('sampler benchmark sink', () => {
  it('throughput benches do not retain emitted events in memory', () => {
    // Regression for MASS-0528: the bench previously built all three loggers
    // with memorySink(), whose write() pushes every event into an uncapped
    // events[] array that the bench never clears. Guard the bench source so an
    // accumulating sink cannot be reintroduced at this location.
    assert.ok(
      benchSource.includes('noopSink()'),
      'sampler.bench.ts must use noopSink() for throughput benches'
    );
    assert.ok(
      !benchSource.includes('memorySink()'),
      'sampler.bench.ts must not use memorySink() (unbounded event accumulation)'
    );
  });

  it('noopSink discards events across many emits without retaining them', async () => {
    // Reproduce the accumulation mechanism: memorySink retains every emit,
    // while the sink the bench now uses discards them instead.
    const accumulating = memorySink();
    const discarding = noopSink();

    const accumulatingLogger = createLoza(
      production('bench').withSink(accumulating).withSampler(sampleAll())
    );
    const discardingLogger = createLoza(
      production('bench').withSink(discarding).withSampler(sampleAll())
    );

    for (let i = 0; i < 1000; i++) {
      const ctxA = accumulatingLogger.startEvent({ event: 'bench.accumulate' });
      accumulatingLogger.finish(ctxA, 'success');
      await accumulatingLogger.emit(ctxA);

      const ctxD = discardingLogger.startEvent({ event: 'bench.discard' });
      discardingLogger.finish(ctxD, 'success');
      await discardingLogger.emit(ctxD);
    }

    // memorySink grows without bound: 1000 emits -> 1000 retained events.
    assert.equal(accumulating.getLength(), 1000);
    // The sink used by the fixed bench exposes no retained-event buffer at all.
    assert.equal((discarding as any).events, undefined);
    assert.equal((discarding as any).getLength, undefined);
  });
});
