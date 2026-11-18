import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { lozaHttpMiddleware } from '../src/middleware/http.ts';
import { lozaMiddleware } from '../src/middleware/express.ts';
import { lozaKoaMiddleware } from '../src/middleware/koa.ts';
import { lozaFastifyPlugin } from '../src/middleware/fastify.ts';
import { lozaHonoMiddleware } from '../src/middleware/hono.ts';
import { lozaNextMiddleware } from '../src/middleware/next.ts';
import { Logger } from '../src/core/logger.ts';

type HttpResponse = {
  statusCode: number;
  end: (...args: unknown[]) => unknown;
  on: (name: string, handler: (err: Error) => void) => void;
};

describe('framework middleware', () => {
  it('records HTTP middleware success and error responses', async () => {
    let errorHandler: ((err: Error) => void) | undefined;
    const errorResponse: HttpResponse = {
      statusCode: 200,
      end() { return 'ignored'; },
      on(name, handler) { if (name === 'error') errorHandler = handler; },
    };
    const req = { method: 'GET', url: '/health', headers: {}, socket: {} };
    let nextCalls = 0;
    const middleware = lozaHttpMiddleware({ service: 'edge' });
    middleware(req, errorResponse, () => { nextCalls++; });
    assert.equal(nextCalls, 1);
    assert.ok(errorHandler);
    errorHandler(new Error('socket failed'));
    await Promise.resolve();

    let ended = false;
    const successResponse: HttpResponse = {
      statusCode: 200,
      end() { ended = true; return 'ended'; },
      on() {},
    };
    lozaHttpMiddleware({ service: 'edge' })(req, successResponse, () => {});
    assert.equal(successResponse.end('body'), 'ended');
    assert.equal(ended, true);

    const res500: HttpResponse = {
      statusCode: 500,
      end() {},
      on() {},
    };
    lozaHttpMiddleware()(req, res500, () => {});
    res500.end();
  });

  it('emits and rethrows when downstream middleware throws synchronously', () => {
    const captured: any[] = [];
    let emitCount = 0;
    const originalStart = Logger.prototype.startHTTPEvent;
    const originalEmit = Logger.prototype.emit;
    (Logger.prototype as any).startHTTPEvent = function (...args: any[]) {
      const ev = originalStart.apply(this, args as any);
      captured.push(ev);
      return ev;
    };
    (Logger.prototype as any).emit = function (...args: any[]) {
      emitCount++;
      return originalEmit.apply(this, args as any);
    };
    try {
      const boom = new Error('sync middleware failure');
      const req = { method: 'GET', url: '/boom', headers: {}, socket: {} };
      const res: HttpResponse = { statusCode: 200, end() {}, on() {} };

      assert.throws(
        () => lozaHttpMiddleware({ service: 'edge' })(req, res, () => { throw boom; }),
        /sync middleware failure/,
      );

      assert.equal(captured.length, 1);
      assert.equal(emitCount, 1, 'event must be emitted when next() throws synchronously');
      assert.equal(captured[0].getEventState(), 'emitted');
      assert.ok(captured[0].error, 'event must record the thrown error');

      // Frameworks (e.g. Express) call res.end after handling the rethrown error;
      // that later completion must not finish/emit the already-emitted event.
      assert.doesNotThrow(() => (res.end as (...args: unknown[]) => unknown)());
      assert.equal(emitCount, 1, 'res.end must not emit the event a second time');
    } finally {
      (Logger.prototype as any).startHTTPEvent = originalStart;
      (Logger.prototype as any).emit = originalEmit;
    }
  });

  it('finishes HTTP events exactly once when error and end both fire', () => {
    const middlewares: Array<[string, (opts: { service?: string }) => (req: any, res: any, next: any) => void]> = [
      ['http', lozaHttpMiddleware],
      ['express', lozaMiddleware],
    ];
    const req = { method: 'GET', url: '/abort', headers: {}, socket: {} };

    for (const [name, create] of middlewares) {
      // Client abort emits 'error' after the response has ended.
      {
        let errorHandler: ((err: Error) => void) | undefined;
        const res: HttpResponse = {
          statusCode: 200,
          end() {},
          on(evt, handler) { if (evt === 'error') errorHandler = handler; },
        };
        create({ service: 'regression' })(req, res, () => {});
        res.end();
        assert.doesNotThrow(() => errorHandler?.(new Error('abort')), `${name}: end then error must not throw`);
      }

      // Client abort emits 'error' before res.end() runs.
      {
        let errorHandler: ((err: Error) => void) | undefined;
        const res: HttpResponse = {
          statusCode: 200,
          end() {},
          on(evt, handler) { if (evt === 'error') errorHandler = handler; },
        };
        create({ service: 'regression' })(req, res, () => {});
        assert.doesNotThrow(() => errorHandler?.(new Error('abort')), `${name}: error handler must not throw`);
        assert.doesNotThrow(() => res.end(), `${name}: end after error must not throw`);
      }
    }
  });

  it('records Express routes from extractor, route path, and request path', () => {
    const responses: number[] = [];
    const makeRes = (statusCode: number): HttpResponse => ({
      statusCode,
      end() { responses.push(statusCode); },
      on() {},
    });
    let nextCalls = 0;
    const extractor = lozaMiddleware({ service: 'api', routeExtractor: () => '/custom' });
    const first = makeRes(200);
    extractor(
      { method: 'POST', path: '/ignored', route: { path: '/route' }, headers: { 'user-agent': 'ua' }, ip: '1.2.3.4' },
      first,
      () => { nextCalls++; },
    );
    first.end();
    const routeResponse = makeRes(404);
    lozaMiddleware()(
      { method: 'GET', path: '/path', route: { path: '' } },
      routeResponse,
      () => { nextCalls++; },
    );
    routeResponse.end();
    const fallbackResponse = makeRes(500);
    lozaMiddleware()(
      { method: 'GET', path: '/path', headers: {}, socket: {} },
      fallbackResponse,
      () => { nextCalls++; },
    );
    fallbackResponse.end();
    assert.equal(nextCalls, 3);
    assert.deepEqual(responses, [200, 404, 500]);
  });

  it('ends the Express response even when an error already finished the event', async () => {
    let errorHandler: ((err: Error) => void) | undefined;
    let ended = false;
    const res: HttpResponse = {
      statusCode: 200,
      end() { ended = true; return 'ended'; },
      on(name, handler) { if (name === 'error') errorHandler = handler; },
    };
    const req = { method: 'GET', path: '/broken', headers: {}, socket: {} };
    lozaMiddleware({ service: 'api' })(req, res, () => {});
    assert.ok(errorHandler);

    // A socket error finishes/emits the event before the response is ended.
    errorHandler!(new Error('socket failed'));
    await Promise.resolve();

    // The override must still end the response instead of throwing a
    // double-finish error from loza.finish.
    assert.equal(res.end('body'), 'ended');
    assert.equal(ended, true);

    // A repeated end must not re-finish the event or swallow the real end.
    assert.equal(res.end(), 'ended');
  });

  it('finishes Koa events after success and error', async () => {
    let successRan = false;
    await lozaKoaMiddleware({ service: 'koa' })(
      { method: 'GET', path: '/ok', route: { path: '/ok' }, headers: {}, socket: {}, status: 201 },
      async () => { successRan = true; },
    );
    assert.equal(successRan, true);
    await assert.doesNotReject(() => lozaKoaMiddleware()(
      { method: 'GET', path: '/bad', headers: {}, socket: {}, status: 500 },
      async () => { throw new Error('boom'); },
    ));
  });

  it('finishes Fastify reply promises on fulfillment and rejection', async () => {
    const success = lozaFastifyPlugin({ service: 'fastify' });
    let fulfilled = false;
    const reply = Object.assign(Promise.resolve().then(() => { fulfilled = true; }), { statusCode: 200 });
    await success(
      { method: 'GET', url: '/ok', routeOptions: { url: '/users/:id' }, headers: {}, socket: {}, ip: '' },
      reply,
    );
    assert.equal(fulfilled, true);

    const failure = lozaFastifyPlugin();
    const failedReply = Object.assign(Promise.reject(new Error('failed')), { statusCode: 503 });
    failedReply.catch(() => {});
    await failure({ method: 'POST', url: '/bad', headers: {}, socket: {}, ip: '127.0.0.1' }, failedReply);
  });

  it('Fastify middleware strips query strings from path, route, and event', async () => {
    const original = Logger.prototype.startHTTPEvent;
    const captured: any[] = [];
    (Logger.prototype as any).startHTTPEvent = function (params: any) {
      captured.push(params);
      return original.call(this, params);
    };
    try {
      const plugin = lozaFastifyPlugin({ service: 'fastify' });
      const makeReply = () => Object.assign(Promise.resolve(), { statusCode: 200 });

      // Fastify exposes a stable route pattern: the query string still must not leak.
      await plugin(
        { method: 'GET', url: '/login?token=super-secret&user=alice', routeOptions: { url: '/login' }, headers: {}, ip: '1.2.3.4' },
        makeReply(),
      );
      // With no route pattern, the fallback route must be the query-stripped pathname.
      await plugin(
        { method: 'POST', url: '/search?q=private-value', headers: {}, ip: '1.2.3.4' },
        makeReply(),
      );

      assert.equal(captured.length, 2);
      assert.equal(captured[0].path, '/login');
      assert.equal(captured[0].route, '/login');
      assert.equal(captured[0].event, 'GET /login');
      assert.equal(captured[1].path, '/search');
      assert.equal(captured[1].route, '/search');
      assert.equal(captured[1].event, 'POST /search');

      for (const params of captured) {
        assert.ok(!String(params.path).includes('?'));
        assert.ok(!String(params.route).includes('?'));
        assert.ok(!String(params.event).includes('?'));
        assert.ok(!JSON.stringify(params).includes('super-secret'));
        assert.ok(!JSON.stringify(params).includes('private-value'));
      }
    } finally {
      (Logger.prototype as any).startHTTPEvent = original;
    }
  });

  it('finishes Hono events on success and thrown errors', async () => {
    const successContext = {
      req: {
        method: 'GET', path: '/ok', routePath: '/route',
        header(name: string) { return name === 'user-agent' ? 'ua' : '10.0.0.1'; },
      },
      res: { status: 200 },
    };
    let called = false;
    await lozaHonoMiddleware({ service: 'hono' })(successContext, async () => { called = true; });
    assert.equal(called, true);
    await lozaHonoMiddleware()(
      { req: { method: 'POST', path: '/bad', header: () => '' }, res: { status: 500 } },
      async () => { throw new Error('bad'); },
    );
  });

  it('returns Next responses and rethrows failures', async () => {
    const middleware = lozaNextMiddleware({ service: 'next' });
    const request = {
      method: '',
      url: '/fallback',
      headers: { get: (name: string) => name === 'user-agent' ? 'ua' : '' },
    };
    const response = { status: 200 };
    assert.equal(await middleware(request, async () => response), response);
    const nextRequest = {
      method: 'POST', nextUrl: { pathname: '/users' },
      headers: { get: () => '10.0.0.2' },
    };
    await assert.rejects(() => middleware(nextRequest, async () => { throw new Error('request failed'); }), /request failed/);
  });
});
