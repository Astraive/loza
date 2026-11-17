import { Logger } from '../core/logger.ts';
import { String as AttrString, Int as AttrInt } from '../core/event.ts';
import { runWithEvent } from '../core/context.ts';

export interface HttpMiddlewareOptions {
  service?: string;
}

export function lozaHttpMiddleware(opts: HttpMiddlewareOptions = {}) {
  const loza = new Logger({ service: opts.service });

  return (req: any, res: any, next: any) => {
    const startedAt = Date.now();

    const ev = loza.startHTTPEvent({
      event: `${req.method} ${req.url}`,
      kind: 'http',
      method: req.method,
      path: req.url,
      service: opts.service,
    });

    loza.enrich(ev,
      AttrString('http.user_agent', req.headers?.['user-agent'] || ''),
      AttrString('http.remote_ip', req.socket?.remoteAddress || ''),
    );

    // A request can settle through res.end, the response 'error' event, or a
    // synchronous throw from downstream middleware. Only settle it once so a
    // later path cannot finish/emit an already-emitted event.
    let settled = false;
    const finishAndEmit = (finalize: () => void) => {
      if (settled) return;
      settled = true;
      finalize();
      loza.emit(ev).catch(() => {});
    };
    // Scope downstream request handling to this event with run(); the context
    // is restored once next() and its async children complete.
    runWithEvent(ev, () => next());

    const originalEnd = res.end;
    res.end = function (...args: any[]) {
      const durationMs = Date.now() - startedAt;
      finishAndEmit(() => {
        const outcome = res.statusCode >= 500 ? 'error' : 'success';
        loza.finish(ev, outcome,
          AttrInt('status_code', res.statusCode),
          AttrInt('duration_ms', durationMs),
        );
      });
      return originalEnd.apply(res, args);
    };

    res.on('error', (err: Error) => {
      finishAndEmit(() => loza.finishError(ev, err));
    });

    try {
      next();
    } catch (err: any) {
      finishAndEmit(() => loza.finishError(ev, err));
      throw err;
    }
  };
}
