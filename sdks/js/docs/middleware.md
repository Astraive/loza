# Middleware

JS framework middleware for automatic HTTP event creation. The SDK provides Express middleware, Node.js `http` module integration, and AsyncLocalStorage context propagation.

## Express Middleware

Import from the sub-path export:

```typescript
import { lozaMiddleware } from '@astraive/loza/middleware/express';
import express from 'express';

const app = express();
app.use(lozaMiddleware({ service: 'my-service' }));

app.get('/users/:id', (req, res) => {
  res.json({ id: req.params.id });
});
```

The middleware:
1. Creates an HTTP event on each request.
2. Enriches with method, path, route, user-agent, remote IP.
3. On response finish, records status code, duration, and outcome.
4. Emits the event asynchronously.

### Options

| Option | Type | Description |
|--------|------|-------------|
| `service` | `string` | Service name for events. |
| `routeExtractor` | `(req) => string` | Custom route extraction function. |

## Node.js http Module

For raw `http.createServer` applications, use the middleware wrapper:

```typescript
import http from 'http';
import { configure, production } from '@astraive/loza';

configure(production('my-service'));

const server = http.createServer((req, res) => {
  // The middleware wraps request handling
  res.end('ok');
});
```

## AsyncLocalStorage Context

The SDK uses Node.js `AsyncLocalStorage` to propagate event context across async operations. Context is always **scoped with `runWithEvent`** (which uses `AsyncLocalStorage.run()`), so it is available to any async function inside the scoped callback and is automatically restored when that work completes. `startEvent` returns the event but does **not** install an ambient context:

```typescript
import { startEvent, enrich, getEvent, hasEvent, finish, emit, runWithEvent } from '@astraive/loza';

async function handleRequest() {
  const ctx = startEvent({ event: 'request', kind: 'http' });

  // Scope the event to this request's work. The context is restored when the
  // callback (and its async children) complete, so it cannot bleed into a
  // later/unrelated request in a long-lived process.
  await runWithEvent(ctx, () => someAsyncWork());
  finish(ctx, 'success');
  await emit(ctx);
}

async function someAsyncWork() {
  if (hasEvent()) {
    const ctx = getEvent();
    // ctx is the same event from handleRequest
  }
}
```

> **Context lifecycle:** framework middleware wraps downstream request handling in `runWithEvent` for you. If you manage the lifecycle yourself, use `runEvent` or `runWithEvent` and keep all work that needs `getEvent()` inside the callback. Do not install an event with `AsyncLocalStorage.enterWith` — an undisposed `enterWith` context leaks into unrelated work across requests. Use `withoutEvent(fn)` to deliberately run a callback with no event context.

## Framework-Specific Notes

### Express

- Middleware is added via `app.use(lozaMiddleware())`.
- Events are emitted after `res.end()` is called.
- Errors on the response stream trigger `finishError`.

### Koa (planned)

- Sub-path export: `loza/middleware/koa`

### Fastify (planned)

- Sub-path export: `loza/middleware/fastify`

## Sub-Path Exports

The SDK exports middleware from sub-paths defined in `package.json`:

```json
{
  "exports": {
    ".": "./dist/index.js",
    "./middleware/express": "./dist/middleware/express.js",
    "./middleware/koa": "./dist/middleware/koa.js",
    "./middleware/fastify": "./dist/middleware/fastify.js",
    "./middleware/http": "./dist/middleware/http.js"
  }
}
```

## Common Behavior

All middleware implementations:

1. **Request start**: Create an event with `startHTTPEvent`.
2. **Enrich**: Add HTTP metadata (method, path, user-agent, remote IP).
3. **Request end**: Record status code, duration, and outcome.
4. **Emit**: Send the event to the configured sink asynchronously.

Configure the global logger before adding middleware:

```typescript
import { configure, production, httpBatchSink } from '@astraive/loza';

configure(
  production('my-service').withSink(
    httpBatchSink({ endpoint: 'http://collector:9308/events' })
  )
);
```
