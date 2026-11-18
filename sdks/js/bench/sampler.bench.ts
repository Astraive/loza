import { bench, describe } from 'vitest';
import {
  createLoza, production, noopSink,
  sampleErrors, sampleRandom, sampleAll,
} from '../src';

describe('sampler', () => {
  // Throughput benches measure sampler overhead only: the emitted events are
  // never read back, so use a non-accumulating sink. A memorySink here would
  // retain every emitted event for the life of the process, growing without
  // bound across bench iterations and distorting timings via GC pressure.
  const errorsLogger = createLoza(
    production('bench').withSink(noopSink()).withSampler(sampleErrors())
  );
  const randomLogger = createLoza(
    production('bench').withSink(noopSink()).withSampler(sampleRandom(0.5))
  );
  const allLogger = createLoza(
    production('bench').withSink(noopSink()).withSampler(sampleAll())
  );

  bench('SampleErrors - error event', () => {
    const ctx = errorsLogger.startEvent({ event: 'bench.sample-error' });
    errorsLogger.finishError(ctx, new Error('boom'));
    errorsLogger.emit(ctx);
  });

  bench('SampleErrors - success event', () => {
    const ctx = errorsLogger.startEvent({ event: 'bench.sample-ok' });
    errorsLogger.finish(ctx, 'success');
    errorsLogger.emit(ctx);
  });

  bench('SampleRandom(0.5)', () => {
    const ctx = randomLogger.startEvent({ event: 'bench.sample-random' });
    randomLogger.finish(ctx, 'success');
    randomLogger.emit(ctx);
  });

  bench('SampleAll', () => {
    const ctx = allLogger.startEvent({ event: 'bench.sample-all' });
    allLogger.finish(ctx, 'success');
    allLogger.emit(ctx);
  });
});
