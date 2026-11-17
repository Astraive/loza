import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import * as sdk from '../src/index.ts';
import { defaultConfig, withOptions } from '../src/config/config.ts';
import { mergeFileConfig } from '../src/config/config-file.ts';

const acceptedResponse = JSON.stringify({
  request_id: 'req-ok', status: 'accepted', accepted: 1, rejected: 0, invalid: 0, acks: [],
});

async function listenServer(handler: (req: http.IncomingMessage, res: http.ServerResponse) => void): Promise<{ server: http.Server; endpoint: string }> {
  const server = http.createServer(handler);
  const gate = Promise.withResolvers<void>();
  server.once('error', gate.reject);
  server.listen(0, '127.0.0.1', () => gate.resolve());
  await gate.promise;
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('server did not expose a port');
  return { server, endpoint: `http://127.0.0.1:${address.port}/events` };
}

async function closeServer(server: http.Server): Promise<void> {
  const gate = Promise.withResolvers<void>();
  server.close(error => error ? gate.reject(error) : gate.resolve());
  await gate.promise;
}

function unreachableSink(overflowPolicy: sdk.OverflowPolicy, maxQueueSize = 3): sdk.HTTPBatchSink {
  return new sdk.HTTPBatchSink({
    endpoint: 'http://127.0.0.1:1/events',
    enableCompression: false,
    ndjson: true,
    retries: 0,
    timeout: 5,
    maxQueueSize,
    batchSize: 100,
    flushIntervalMs: 100000,
    overflowPolicy,
  });
}

describe('HTTPBatchSink bounded buffer', () => {
  it('drop-oldest keeps the buffer bounded and reports every drop', async () => {
    let notified = 0;
    const sink = new sdk.HTTPBatchSink({
      endpoint: 'http://127.0.0.1:1/events',
      enableCompression: false,
      retries: 0,
      timeout: 5,
      maxQueueSize: 3,
      batchSize: 100,
      flushIntervalMs: 100000,
      overflowPolicy: 'drop-oldest',
      statsHandler: { onBufferOverflow: () => { notified++; } },
    });
    try {
      for (let i = 0; i < 10; i++) await sink.write(`{"n":${i}}`);
      assert.equal(sink.queueSize(), 3);
      assert.deepEqual(sink.metrics, {
        queued: 3,
        capacity: 3,
        dropped: 7,
        droppedOnOverflow: 7,
        droppedOnDeliveryFailure: 0,
      });
      assert.equal(sink.droppedEvents, 7);
      assert.equal(notified, 7);
    } finally {
      sink.pause();
    }
  });

  it('drop-newest discards incoming events when the buffer is full', async () => {
    const sink = unreachableSink('drop-newest');
    try {
      for (let i = 0; i < 8; i++) await sink.write(`{"n":${i}}`);
      assert.equal(sink.queueSize(), 3);
      assert.equal(sink.metrics.droppedOnOverflow, 5);
      assert.equal(sink.metrics.droppedOnDeliveryFailure, 0);
    } finally {
      sink.pause();
    }
  });

  it('overflow policy decides which events survive', async () => {
    const bodies: string[] = [];
    const { server, endpoint } = await listenServer((req, res) => {
      let body = '';
      req.on('data', chunk => { body += chunk.toString(); });
      req.on('end', () => {
        bodies.push(body);
        res.statusCode = 500;
        res.end('down');
      });
    });
    try {
      const make = (overflowPolicy: sdk.OverflowPolicy) => new sdk.HTTPBatchSink({
        endpoint,
        enableCompression: false,
        ndjson: true,
        retries: 0,
        maxQueueSize: 3,
        batchSize: 100,
        flushIntervalMs: 100000,
        overflowPolicy,
      });

      const keepNewest = make('drop-oldest');
      for (let i = 0; i < 5; i++) await keepNewest.write(JSON.stringify({ n: i }));
      await keepNewest.flush().catch(() => {});

      const keepOldest = make('drop-newest');
      for (let i = 0; i < 5; i++) await keepOldest.write(JSON.stringify({ n: i }));
      await keepOldest.flush().catch(() => {});

      keepNewest.pause();
      keepOldest.pause();
      assert.deepEqual(bodies[0].trim().split('\n').map(line => JSON.parse(line).n), [2, 3, 4]);
      assert.deepEqual(bodies[1].trim().split('\n').map(line => JSON.parse(line).n), [0, 1, 2]);
    } finally {
      await closeServer(server);
    }
  });

  it('block policy sheds after its wait times out instead of growing the buffer', async () => {
    const sink = new sdk.HTTPBatchSink({
      endpoint: 'http://127.0.0.1:1/events',
      enableCompression: false,
      ndjson: true,
      retries: 0,
      timeout: 5,
      maxQueueSize: 2,
      batchSize: 100,
      flushIntervalMs: 100000,
      overflowPolicy: 'block',
      blockTimeoutMs: 20,
    });
    try {
      await sink.write('{"n":0}');
      await sink.write('{"n":1}');
      assert.equal(sink.queueSize(), 2);
      await sink.write('{"n":2}');
      assert.equal(sink.queueSize(), 2);
      assert.equal(sink.metrics.droppedOnOverflow, 1);
    } finally {
      sink.pause();
    }
  });

  it('never re-prepends more than the capacity during sustained delivery failure', async () => {
    let requests = 0;
    const { server, endpoint } = await listenServer((_req, res) => {
      requests++;
      res.statusCode = 500;
      res.end('outage');
    });
    try {
      const sink = new sdk.HTTPBatchSink({
        endpoint,
        enableCompression: false,
        ndjson: true,
        retries: 0,
        baseDelay: 0,
        maxDelay: 0,
        timeout: 200,
        maxQueueSize: 32,
        batchSize: 64,
        flushIntervalMs: 5,
        overflowPolicy: 'drop-oldest',
      });
      const heapBefore = process.memoryUsage().heapUsed;
      try {
        for (let i = 0; i < 2000; i++) {
          await sink.write(JSON.stringify({ n: i, payload: 'x'.repeat(128) }));
          assert.ok(sink.queueSize() <= 32, `buffer grew to ${sink.queueSize()}`);
        }
        await sink.flush().catch(() => {});
      } finally {
        sink.pause();
      }
      const heapGrowth = process.memoryUsage().heapUsed - heapBefore;
      assert.ok(requests > 0);
      assert.equal(sink.metrics.capacity, 32);
      assert.ok(sink.queueSize() <= 32);
      assert.ok(sink.metrics.dropped > 0, 'sustained failure should record shed events');
      assert.ok(sink.metrics.droppedOnDeliveryFailure >= 0);
      assert.ok(heapGrowth < 128 * 1024 * 1024, `heap grew by ${heapGrowth} bytes`);
    } finally {
      await closeServer(server);
    }
  });

  it('wires async.queueSize and overflowPolicy from configuration', async () => {
    const cfg = withOptions(defaultConfig(), { asyncQueueSize: 7, overflowPolicy: 'block' });
    assert.equal(cfg.async.queueSize, 7);
    assert.equal(cfg.async.overflowPolicy, 'block');
    assert.equal(defaultConfig().async.overflowPolicy, 'drop-oldest');

    const built = sdk.test('svc').withQueueSize(9).withOverflowPolicy('drop-newest').build();
    assert.equal(built.async.queueSize, 9);
    assert.equal(built.async.overflowPolicy, 'drop-newest');

    assert.equal(mergeFileConfig(defaultConfig(), { async: { overflow_policy: 'block' } }).async.overflowPolicy, 'block');
    assert.equal(mergeFileConfig(defaultConfig(), { async: { overflow_policy: 'bogus' } }).async.overflowPolicy, 'drop-oldest');

    const logger = sdk.createLoza({
      service: 'overflow',
      collectorUrl: 'http://127.0.0.1:1/events',
      maxRetries: 0,
      timeoutMs: 5,
      enableCompression: false,
      batchSize: 100,
      flushIntervalMs: 100000,
      async: {
        enabled: true,
        queueSize: 4,
        overflowPolicy: 'drop-newest',
        workers: 1,
        maxBatchBytes: 1024,
        flushIntervalMs: 100000,
      },
    });
    try {
      for (let i = 0; i < 10; i++) await logger.info('overflow.event', { n: i });
      assert.equal(logger.queueSize(), 4);
    } finally {
      logger.pause();
    }
  });
});
