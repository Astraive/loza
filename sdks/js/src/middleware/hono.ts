import { Logger } from '../core/logger.ts';
import { String as AttrString, Int as AttrInt } from '../core/event.ts';
import { runWithEvent } from '../core/context.ts';

export interface HonoMiddlewareOptions {
  service?: string;
}

export function lozaHonoMiddleware(opts: HonoMiddlewareOptions = {}) {
  const loza = new Logger({ service: opts.service });

  return async (c: any, next: any) => {
    const startedAt = Date.now();
    const route = c.req.routePath || c.req.path || '';

    const ev = loza.startHTTPEvent({
      event: `${c.req.method} ${route}`,
      kind: 'http',
      method: c.req.method,
      path: c.req.path,
      route,
      service: opts.service,
    });

    // Use the framework-resolved remote address (socket peer / trusted proxy)
    // instead of the raw, client-controlled `x-forwarded-for` header. Hono
    // populates `c.req.ip` using its own trusted-proxy rules, so a client
    // cannot forge the recorded IP by setting that header.
    const remoteIP = c.req.ip || '';

    loza.enrich(ev,
      AttrString('http.user_agent', c.req.header('user-agent') || ''),
      AttrString('http.remote_ip', remoteIP),
    );

    try {
      // Scope downstream handling with run(); context is restored afterwards.
      await runWithEvent(ev, () => next());
      const durationMs = Date.now() - startedAt;
      const outcome = c.res.status >= 500 ? 'error' : 'success';
      loza.finish(ev, outcome,
        AttrInt('status_code', c.res.status),
        AttrInt('duration_ms', durationMs),
      );
    } catch (err: any) {
      const durationMs = Date.now() - startedAt;
      loza.finishError(ev, err,
        AttrInt('duration_ms', durationMs),
      );
    }
    await loza.emit(ev).catch(() => {});
  };
}
