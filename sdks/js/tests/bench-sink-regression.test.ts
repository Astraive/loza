import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { createLoza, production, noopSink, memorySink } from '../src/index.ts';

const benchPath = fileURLToPath(new URL('../bench/middleware.bench.ts', import.meta.url));

describe('middleware bench sink (MASS-0529)', () => {
  it('middleware bench uses a discarding sink, not the accumulating memorySink', () => {
    const source = readFileSync(benchPath, 'utf8');
    assert.match(source, /noopSink\(\)/, 'middleware bench should configure noopSink()');
    assert.doesNotMatch(source, /memorySink\(\)/, 'middleware bench must not use memorySink()');
  });

  it('reproduces the defect: memorySink grows by one event per emit', async () => {
    const sink = memorySink();
    const logger = createLoza(production('bench').withSink(sink));
    const iterations = 250;
    for (let i = 0; i < iterations; i++) {
      const ctx = logger.startEvent({ event: 'bench.middleware' });
      logger.finish(ctx, 'success');
      await logger.emit(ctx);
    }
    assert.equal(sink.getLength(), iterations);
  });

  it('corrected behavior: the bench sink retains no events across the same emits', async () => {
    const sink = noopSink();
    const logger = createLoza(production('bench').withSink(sink));
    for (let i = 0; i < 250; i++) {
      const ctx = logger.startEvent({ event: 'bench.middleware' });
      logger.finish(ctx, 'success');
      await logger.emit(ctx);
    }
    // A discarding sink exposes no retained-event state at all.
    assert.equal('getLength' in sink, false);
    assert.equal('getEvents' in sink, false);
  });
});
