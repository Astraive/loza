import { Logger } from '../core/logger.ts';
import { String as AttrString, Int as AttrInt } from '../core/event.ts';

export interface FastifyPluginOptions {
  service?: string;
}

export function lozaFastifyPlugin(opts: FastifyPluginOptions = {}) {
  const loza = new Logger({ service: opts.service });

  return async (request: any, reply: any) => {
    const startedAt = Date.now();
    // Fastify's request.url includes the query string. Strip it before recording
    // so secrets/PII in query params never land in telemetry attributes.
    const path = request.url?.split('?')[0] || '';
    // Prefer Fastify's stable route pattern (e.g. /users/:id) over the raw URL;
    // when it is unavailable, fall back to the query-stripped pathname.
    const route = request.routeOptions?.url || path;

    // The event is passed explicitly to finish/emit below. Fastify runs
    // downstream handlers after this hook returns, so there is no callback to
    // scope with runWithEvent here; unlike Express/Koa/Hono/Next, this hook
    // does not install an async context (which was the source of the
    // cross-request bleed caused by enterWith).
    const ev = loza.startHTTPEvent({
      event: `${request.method} ${route}`,
      kind: 'http',
      method: request.method,
      path,
      route,
      service: opts.service,
    });

    loza.enrich(ev,
      AttrString('http.user_agent', request.headers?.['user-agent'] || ''),
      AttrString('http.remote_ip', request.ip || request.socket?.remoteAddress || ''),
    );

    reply.then?.(
      () => {
        const durationMs = Date.now() - startedAt;
        const outcome = reply.statusCode >= 500 ? 'error' : 'success';
        loza.finish(ev, outcome,
          AttrInt('status_code', reply.statusCode),
          AttrInt('duration_ms', durationMs),
        );
        loza.emit(ev).catch(() => {});
      },
      (err: any) => {
        loza.finishError(ev, err);
        loza.emit(ev).catch(() => {});
      },
    );
  };
}
