import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { CollectorClient } from '../src/collector/client.ts';

async function startCollector(onBody: (body: unknown) => void): Promise<{ server: http.Server; url: string }> {
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (chunk: Buffer) => chunks.push(chunk));
    req.on('end', () => {
      onBody(JSON.parse(Buffer.concat(chunks).toString('utf-8')));
      res.statusCode = 200;
      res.setHeader('content-type', 'application/json');
      res.end(JSON.stringify({ columns: [], rows: [] }));
    });
  });
  const gate = Promise.withResolvers<void>();
  server.once('error', gate.reject);
  server.listen(0, '127.0.0.1', () => gate.resolve());
  await gate.promise;
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('missing address');
  return { server, url: `http://127.0.0.1:${address.port}` };
}

async function stopCollector(server: http.Server): Promise<void> {
  const gate = Promise.withResolvers<void>();
  server.close(error => error ? gate.reject(error) : gate.resolve());
  await gate.promise;
}

describe('CollectorClient.queryLql limit normalization', () => {
  it('clamps zero, fractional, NaN, and negative limits to the minimum of 1', async () => {
    const sentLimits: unknown[] = [];
    const { server, url } = await startCollector(body => {
      sentLimits.push((body as { limit: unknown }).limit);
    });
    try {
      const client = new CollectorClient({ url, enableCompression: false });
      await client.queryLql('select', {}, 0);
      await client.queryLql('select', {}, 0.5);
      await client.queryLql('select', {}, Number.NaN);
      await client.queryLql('select', {}, -5);
      assert.deepEqual(sentLimits, [1, 1, 1, 1]);
    } finally {
      await stopCollector(server);
    }
  });

  it('truncates fractional limits and clamps large limits to 1000', async () => {
    const sentLimits: unknown[] = [];
    const { server, url } = await startCollector(body => {
      sentLimits.push((body as { limit: unknown }).limit);
    });
    try {
      const client = new CollectorClient({ url, enableCompression: false });
      await client.queryLql('select', {}, 5.9);
      await client.queryLql('select', {}, 1001);
      await client.queryLql('select');
      assert.deepEqual(sentLimits, [5, 1000, 1000]);
    } finally {
      await stopCollector(server);
    }
  });
});
