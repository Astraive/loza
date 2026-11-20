import { Logger } from '../core/logger.ts';
import { String as AttrString, Int as AttrInt } from '../core/event.ts';
import { runWithEvent } from '../core/context.ts';

export interface ExpressMiddlewareOptions {
  service?: string;
  routeExtractor?: (req: any) => string;
}

export function lozaMiddleware(opts: ExpressMiddlewareOptions = {}) {
  const loza = new Logger({ service: opts.service });

  return (req: any, res: any, next: any) => {
    const startedAt = Date.now();
    const route = opts.routeExtractor?.(req) || req.route?.path || req.path || '';

    const ctx = loza.startHTTPEvent({
      event: `${req.method} ${route}`,
      kind: 'http',
      method: req.method,
      path: req.path,
      route,
      service: opts.service,
    });

    loza.enrich(ctx,
      AttrString('http.user_agent', req.headers?.['user-agent'] || ''),
      AttrString('http.remote_ip', req.ip || req.socket?.remoteAddress || ''),
    );

    // Only one of the `error` and `end` handlers may settle the event; both
    // finish it, and Event.finish throws once it is already finished/emitted.
    let settled = false;
    const settle = (finalize: () => void): void => {
      if (settled) return;
      settled = true;
      try {
        finalize();
      } finally {
        loza.emit(ctx).catch(() => {});
      }
    };
    // Install the response hooks before invoking downstream code. A handler
    // that calls res.end() synchronously inside next() must be intercepted.
    const originalEnd = res.end;
    res.end = function (...args: any[]) {
      const durationMs = Date.now() - startedAt;
      const outcome = res.statusCode >= 500 ? 'error' : 'success';
      try {
        settle(() => loza.finish(ctx, outcome,
          AttrInt('status_code', res.statusCode),
          AttrInt('duration_ms', durationMs),
        ));
      } finally {
        // Telemetry must never prevent the HTTP response from ending.
        return originalEnd.apply(res, args);
      }
    };

    res.on('error', (err: Error) => {
      settle(() => loza.finishError(ctx, err));
    });

    // Scope downstream request handling to this event with run(). The context
    // is restored when next() and its async children complete, so it cannot
    // bleed into unrelated work or a later request. This runs only after the
    // res.end / error hooks above are in place.
    runWithEvent(ctx, () => next());
  };
}
