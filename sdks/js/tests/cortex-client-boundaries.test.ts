import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {
  CortexClient, CortexHttpError, normalizeIncidentContext, normalizeGraphView, normalizeRemediation,
  validateIncidentContext, validateGraphView, validateRemediation, validateFeedback,
} from '../src/cortex/client.ts';

async function startCortex(): Promise<{ server: http.Server; url: string }> {
  const server = http.createServer((req, res) => {
    res.statusCode = req.url === '/readyz' ? 503 : 200;
    res.setHeader('content-type', 'application/json');
    if (req.url === '/reconstruct') res.end(JSON.stringify({ incident_id: ' incident ', timestamp: '', confidence: 2, similar_incidents: [{ id: 1 }] }));
    else if (req.url?.startsWith('/graph/')) res.end(JSON.stringify({ nodes: [{ id: ' node ' }], edges: [{ source: 'node', target: 'node' }] }));
    else res.end('{}');
  });
  const gate = Promise.withResolvers<void>();
  server.once('error', gate.reject);
  server.listen(0, '127.0.0.1', () => gate.resolve());
  await gate.promise;
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('missing address');
  return { server, url: `http://127.0.0.1:${address.port}` };
}

async function stopCortex(server: http.Server): Promise<void> {
  const gate = Promise.withResolvers<void>();
  server.close(error => error ? gate.reject(error) : gate.resolve());
  await gate.promise;
}

describe('Cortex client boundaries', () => {
  it('normalizes and validates context, graph, remediation, and feedback values', () => {
    const context = { incident_id: ' id ', timestamp: '', confidence: 2, related_services: [' api ', ''] };
    normalizeIncidentContext(context);
    assert.equal(context.incident_id, 'id');
    assert.equal(context.confidence, 1);
    assert.deepEqual(context.related_services, ['api', '']);
    assert.ok(context.timestamp);
    normalizeIncidentContext({ incident_id: null, timestamp: null, confidence: 'unknown', related_services: {} });
    normalizeGraphView({ nodes: null });
    normalizeGraphView({ nodes: [{ id: 1 }] });
    assert.doesNotThrow(() => validateGraphView({ nodes: [], edges: [{ from_node_id: 'a', to_node_id: 'b' }] }));
    normalizeRemediation({ remediation_id: null, incident_id: null, action: null, operator: null, timestamp: null });
    assert.throws(() => validateIncidentContext(null), /nil/);
    assert.throws(() => validateIncidentContext({}), /incident_id/);
    assert.throws(() => validateIncidentContext({ incident_id: 'id' }), /timestamp/);
    assert.throws(() => validateIncidentContext({ incident_id: 'id', timestamp: 'now', confidence: 2 }), /confidence/);
    assert.throws(() => validateGraphView(null), /nil/);
    assert.throws(() => validateGraphView({ nodes: [{}], edges: [] }), /node 0/);
    assert.throws(() => validateGraphView({ nodes: [], edges: [{}] }), /source/);
    assert.throws(() => validateGraphView({ nodes: [], edges: [{ source: 'a' }] }), /target/);
    assert.throws(() => validateRemediation(null), /nil/);
    assert.throws(() => validateRemediation({}), /incident_id/);
    assert.throws(() => validateRemediation({ incident_id: 'id' }), /action/);
    assert.throws(() => validateFeedback(null), /nil/);
    assert.throws(() => validateFeedback({}), /incident_id/);
    assert.throws(() => validateFeedback({ incident_id: 'id' }), /outcome/);
  });

  it('does not inject optional fields into caller-owned remediation objects', async () => {
    // Direct normalization must not add omitted optional fields.
    const direct: any = { incident_id: ' inc ', action: ' restart ' };
    normalizeRemediation(direct);
    assert.deepEqual(Object.keys(direct).sort(), ['action', 'incident_id']);
    assert.equal(direct.incident_id, 'inc');
    assert.equal(direct.action, 'restart');
    assert.equal('remediation_id' in direct, false);
    assert.equal('operator' in direct, false);
    assert.equal('timestamp' in direct, false);

    // recordRemediation must leave the caller's object untouched and must not
    // send values for optional fields that were omitted.
    const bodies: string[] = [];
    const server = http.createServer((req, res) => {
      let body = '';
      req.on('data', chunk => { body += chunk.toString(); });
      req.on('end', () => {
        bodies.push(body);
        res.statusCode = 200;
        res.end('{}');
      });
  it('does not transmit the API key to a plaintext remote host', () => {
    // Regression: an http:// URL used to send x-api-key without TLS.
    assert.throws(
      () => new CortexClient({ url: 'http://example.com', apiKey: 'secret' }),
      /require HTTPS/,
    );
    assert.throws(
      () => new CortexClient({ url: 'http://cortex.internal:9308/', apiKey: 'secret' }),
      /require HTTPS/,
    );
    // No credential, TLS, and loopback endpoints remain allowed.
    assert.doesNotThrow(() => new CortexClient({ url: 'http://example.com' }));
    assert.doesNotThrow(() => new CortexClient({ url: 'https://example.com', apiKey: 'secret' }));
    assert.doesNotThrow(() => new CortexClient({ url: 'http://localhost:9308', apiKey: 'secret' }));
    assert.doesNotThrow(() => new CortexClient({ url: 'http://127.0.0.1:9308', apiKey: 'secret' }));
    assert.doesNotThrow(() => new CortexClient({ url: 'http://[::1]:9308', apiKey: 'secret' }));
  });

  it('still sends the API key header to an allowed loopback endpoint', async () => {
    let seenKey: string | undefined;
    const server = http.createServer((req, res) => {
      seenKey = req.headers['x-api-key'] as string | undefined;
      res.statusCode = 200;
      res.end('{}');
    });
    const gate = Promise.withResolvers<void>();
    server.once('error', gate.reject);
    server.listen(0, '127.0.0.1', () => gate.resolve());
    await gate.promise;
    const address = server.address();
    if (!address || typeof address === 'string') throw new Error('missing address');
    try {
      const client = new CortexClient({ url: `http://127.0.0.1:${address.port}` });
      const data: any = { incident_id: ' inc ', action: ' restart ' };
      await client.recordRemediation(data);
      assert.deepEqual(data, { incident_id: ' inc ', action: ' restart ' });
      assert.equal(bodies.length, 1);
      const sent = JSON.parse(bodies[0]);
      assert.equal(sent.incident_id, 'inc');
      assert.equal(sent.action, 'restart');
      assert.equal('remediation_id' in sent, false);
      assert.equal('operator' in sent, false);
      assert.equal('timestamp' in sent, false);
    } finally {
      await new Promise<void>((resolve, reject) => server.close(err => err ? reject(err) : resolve()));
    try {
      const address = server.address();
      if (!address || typeof address === 'string') throw new Error('missing address');
      const client = new CortexClient({ url: `http://127.0.0.1:${address.port}`, apiKey: 'secret' });
      assert.equal(await client.health(), true);
      assert.equal(seenKey, 'secret');
    } finally {
      await stopCortex(server);
    }
  });

  it('calls health, readiness, ingest, graph, reconstruction, and feedback APIs', async () => {
    const { server, url } = await startCortex();
    try {
      const client = new CortexClient({ url: `${url}/`, apiKey: 'key', maxResponseBytes: 10000 });
      assert.equal(await client.health(), true);
      assert.equal(await client.ready(), false);
      await client.ingestBatch([{ event: 'one' }]);
      await client.ingestJsonl([{ event: 'one' }, { event: 'two' }]);
      const reconstruction = await client.reconstruct('incident');
      assert.equal(reconstruction.incident_id, 'incident');
      assert.equal(reconstruction.confidence, 1);
      assert.deepEqual(await client.similarIncidents('incident', 1), [{ id: 1 }]);
      assert.deepEqual(await client.serviceGraph('checkout'), { nodes: [{ id: 'node' }], edges: [{ source: 'node', target: 'node' }] });
      assert.deepEqual(await client.incidentGraph('incident id'), { nodes: [{ id: 'node' }], edges: [{ source: 'node', target: 'node' }] });
      await client.recordRemediation({ incident_id: 'id', action: 'fix' });
      await client.recordFeedback({ incident_id: 'id', outcome: 'resolved' });
      const unreachable = new CortexClient({ url: 'http://127.0.0.1:1', timeout: 10 });
      assert.equal(await unreachable.health(), false);
    } finally {
      await stopCortex(server);
    }
  });

  it('rejects writes and reads when cortex returns an error status', async () => {
    const server = http.createServer((_req, res) => {
      res.statusCode = 500;
      res.setHeader('content-type', 'application/json');
      res.end(JSON.stringify({ error: 'bad api key' }));
  it('preserves multi-byte UTF-8 characters split across response chunks', async () => {
    const body = JSON.stringify({
      incident_id: 'incident 🚀 fire',
      timestamp: '2026-01-01T00:00:00Z',
      confidence: 0.5,
      note: 'café ☕',
    });
    const payload = Buffer.from(body, 'utf8');
    // 🚀 is a 4-byte UTF-8 sequence (F0 9F 9A 80). Split one byte into it so a
    // per-chunk toString() would decode two broken halves as replacement chars.
    const rocketByteOffset = Buffer.byteLength(body.slice(0, body.indexOf('🚀')), 'utf8');
    const splitAt = rocketByteOffset + 1;

    const server = http.createServer((_req, res) => {
      res.statusCode = 200;
      res.setHeader('content-type', 'application/json');
      res.setHeader('content-length', payload.length);
      res.write(payload.subarray(0, splitAt));
      // A gap forces the two halves to arrive as distinct 'data' events.
      setTimeout(() => {
        res.write(payload.subarray(splitAt));
        res.end();
      }, 50);
    });
    const gate = Promise.withResolvers<void>();
    server.once('error', gate.reject);
    server.listen(0, '127.0.0.1', () => gate.resolve());
    await gate.promise;
    const address = server.address();
    if (!address || typeof address === 'string') throw new Error('missing address');
    const client = new CortexClient({ url: `http://127.0.0.1:${address.port}`, apiKey: 'bad' });
    try {
      // Every write/void caller must surface the failure instead of resolving silently.
      await assert.rejects(client.ingestBatch([{ event: 'one' }]), (err: unknown) => {
        assert.ok(err instanceof CortexHttpError);
        assert.equal(err.status, 500);
        assert.match(err.message, /cortex returned 500/);
        assert.match(err.message, /bad api key/);
        assert.match(err.body, /bad api key/);
        return true;
      });
      await assert.rejects(client.ingestJsonl([{ event: 'one' }]), /cortex returned 500/);
      await assert.rejects(client.recordRemediation({ incident_id: 'id', action: 'fix' }), /cortex returned 500/);
      await assert.rejects(client.recordFeedback({ incident_id: 'id', outcome: 'resolved' }), /cortex returned 500/);
      // Reads go through the same request path and must fail too.
      await assert.rejects(client.reconstruct('incident'), /cortex returned 500/);
      // Health/readiness probes still resolve to false rather than throwing.
      assert.equal(await client.health(), false);
      assert.equal(await client.ready(), false);
    try {
      const address = server.address();
      if (!address || typeof address === 'string') throw new Error('missing address');
      const client = new CortexClient({ url: `http://127.0.0.1:${address.port}` });
      const result = await client.reconstruct('incident');
      assert.equal(result.incident_id, 'incident 🚀 fire');
      assert.equal((result as any).note, 'café ☕');
    } finally {
      await stopCortex(server);
    }
  });
});
