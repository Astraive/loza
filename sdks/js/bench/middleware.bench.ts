import { bench, describe } from 'vitest';
import http from 'http';
import { createLoza, production, noopSink } from '../src';

describe('middleware', () => {
  // Use a discarding sink: emit() runs on every iteration, and an in-memory
  // sink would retain every encoded event for the whole run (unbounded growth).
  const logger = createLoza(production('bench').withSink(noopSink()));

  bench('http request capture simulation', () => {
    // Simulate what middleware does per request
    const ctx = logger.startEvent({
      event: 'GET /api/users',
      kind: 'http',
      method: 'GET',
      path: '/api/users',
      route: '/api/users',
    });
    logger.enrich(ctx,
      { key: 'http.user_agent', value: 'Mozilla/5.0', kind: 'string' },
      { key: 'http.remote_ip', value: '127.0.0.1', kind: 'string' },
    );
    logger.finish(ctx, 'success',
      { key: 'status_code', value: 200, kind: 'int' },
      { key: 'duration_ms', value: 15, kind: 'int' },
    );
    logger.emit(ctx);
  });

  bench('startEvent only (no middleware overhead)', () => {
    const ctx = logger.startEvent({ event: 'bench.plain' });
    logger.finish(ctx, 'success');
    logger.emit(ctx);
  });
});
