import type { Sink } from './sink.ts';
import fs from 'node:fs';
import http from 'node:http';
import https from 'node:https';
import { gzip as zlibGzip } from 'node:zlib';
import { promisify } from 'node:util';
import { parseCollectorResponse } from '../generated/spec-contract.ts';
import type { CollectorResponse } from '../generated/spec-contract.ts';
import { isLocalhost, isPublicDSNUsername } from '../config/dsn.ts';
import { SDK_VERSION } from '../config/version.ts';
const gzipAsync = promisify(zlibGzip);

/** Stats handler for collector acknowledgements. */
export interface StatsHandler {
  onCollectorAck?(data: {
    acks: CollectorResponse['acks'];
    errors: CollectorResponse['errors'];
    requestId: string;
    deduped: number;
  }): void;
}

export interface DeliveryFailureHandler extends StatsHandler {
  onDeliveryFailed?(event: unknown, error: unknown): void;
}

/** Stdout sink — writes NDJSON to process.stdout. */
export class StdoutSink implements Sink {
  name() { return 'stdout'; }
  write(encoded: string): void {
    process.stdout.write(encoded + '\n');
  }
  flush() {}
  close() {}
}

/** Stderr sink — writes NDJSON to process.stderr. */
export class StderrSink implements Sink {
  name() { return 'stderr'; }
  write(encoded: string): void {
    process.stderr.write(encoded + '\n');
  }
  flush() {}
  close() {}
}

export class FileSink implements Sink {
  private path: string;
  constructor(path: string) {
    this.path = path;
  }
  name() { return 'file'; }
  write(encoded: string): void { fs.appendFileSync(this.path, encoded + '\n'); }
  flush() {}
  close() {}
}

export class RotatingFileSink implements Sink {
  private path: string;
  private maxBytes: number;
  private maxBackups: number;
  private currentSize: number;
  private writeCount: number;

  constructor(path: string, maxBytes?: number, maxBackups?: number) {
    this.path = path;
    this.maxBytes = maxBytes ?? 100 * 1024 * 1024;
    this.maxBackups = maxBackups ?? 5;
    this.currentSize = this.getFileSize();
    this.writeCount = 0;
  }

  name() { return 'rotating_file'; }

  write(encoded: string): void {
    const line = encoded + '\n';
    const lineBytes = Buffer.byteLength(line, 'utf-8');

    if (this.currentSize + lineBytes > this.maxBytes) {
      this.rotate();
    }

    fs.appendFileSync(this.path, line);
    this.currentSize += lineBytes;
    this.writeCount++;
  }

  flush() {}
  close() {}

  private getFileSize(): number {
    try {
      return fs.statSync(this.path).size;
    } catch {
      return 0;
    }
  }

  private rotate(): void {
    // Keep milliseconds and append a numeric suffix on collision so two
    // rotations in the same second (or millisecond) cannot resolve to the
    // same destination. `fs.renameSync` overwrites on POSIX, so reusing a
    // name would silently discard the earlier backup.
    const timestamp = new Date().toISOString().replace(/[:.]/g, '-');
    let backupPath = `${this.path}.${timestamp}`;
    let suffix = 1;
    while (fs.existsSync(backupPath)) {
      backupPath = `${this.path}.${timestamp}.${suffix++}`;
    }
    try {
      fs.renameSync(this.path, backupPath);
    } catch {
      // File might not exist yet — carry on
    }
    this.currentSize = 0;
    this.pruneBackups();
  }

  private pruneBackups(): void {
    const dir = this.path.substring(0, this.path.lastIndexOf('/') + 1) || '.';
    const base = this.path.substring(this.path.lastIndexOf('/') + 1);
    try {
      const files = fs.readdirSync(dir)
        .filter(f => f.startsWith(base + '.') && f !== base)
        .map(f => ({
          name: f,
          mtime: fs.statSync(`${dir}/${f}`).mtimeMs,
        }))
        .sort((a, b) => b.mtime - a.mtime);

      for (const file of files.slice(this.maxBackups)) {
        fs.unlinkSync(`${dir}/${file.name}`);
      }
    } catch {
      // Best-effort cleanup
    }
  }
}

/** Noop sink — discards all events. */
export class NoopSink implements Sink {
  name() { return 'noop'; }
  write(_encoded: string): void {}
  flush() {}
  close() {}
}

/** Memory sink — captures events in memory for testing. */
export class MemorySink implements Sink {
  private events: string[] = [];

  name() { return 'memory'; }
  write(encoded: string): void {
    this.events.push(encoded);
  }
  flush() {}
  close() {}

  getEvents(): string[] { return [...this.events]; }
  getLength(): number { return this.events.length; }
  clear(): void { this.events = []; }
}

/** HTTP batch sink — sends events to loza-collector via HTTP. */
export class HTTPBatchSink implements Sink {
  private endpoint: string;
  private apiKey: string;
  private username: string;
  private password: string;
  private authHeader: string;
  private sdkName: string;
  private sdkVersion: string;
  private service: string;
  private timeout: number;
  private retries: number;
  private baseDelay: number;
  private maxDelay: number;
  private enableCompression: boolean;
  private ndjson: boolean;
  private buffer: string[] = [];
  private flushTimer: ReturnType<typeof setTimeout> | null = null;
  private flushTail: Promise<void> = Promise.resolve();
  private closed = false;
  private batchSize: number;
  private flushIntervalMs: number;
  private statsHandler: StatsHandler | null;
  private _lastResponse: CollectorResponse | null = null;

  constructor(opts: {
    endpoint: string;
    apiKey?: string;
    username?: string;
    password?: string;
    authHeader?: string;
    sdkName?: string;
    sdkVersion?: string;
    service?: string;
    timeout?: number;
    retries?: number;
    baseDelay?: number;
    maxDelay?: number;
    enableCompression?: boolean;
    ndjson?: boolean;
    batchSize?: number;
    flushIntervalMs?: number;
    statsHandler?: StatsHandler;
  }) {
    this.endpoint = opts.endpoint;
    const endpoint = new URL(this.endpoint);
    if (endpoint.username || endpoint.password) {
      throw new Error('invalid HTTP sink endpoint: credentials must not be embedded in the URL');
    }
    this.apiKey = opts.apiKey || '';
    this.username = opts.username || '';
    this.password = opts.password || '';
    if (!this.username && this.password) {
      throw new Error('invalid HTTP sink options: Basic password requires a username');
    }
    if (this.username && !this.password && !isPublicDSNUsername(this.username)) {
      throw new Error('invalid HTTP sink options: Basic credentials require a password unless username is an lz_pub_ capability');
    }
    if ((this.apiKey || this.username) &&
      endpoint.protocol === 'http:' &&
      !['localhost', '127.0.0.1', '::1'].includes(endpoint.hostname)) {
      throw new Error('invalid HTTP sink options: credentials require HTTPS (HTTP is allowed only for localhost)');
      !isLocalhost(endpoint.hostname)) {
      throw new Error('invalid HTTP sink options: Basic credentials require HTTPS (HTTP is allowed only for localhost)');
    }
    this.authHeader = opts.authHeader || 'Authorization';
    this.sdkName = opts.sdkName || 'loza-js';
    this.sdkVersion = opts.sdkVersion || SDK_VERSION;
    this.service = opts.service || '';
    this.timeout = opts.timeout || 2000;
    this.retries = opts.retries ?? 3;
    this.baseDelay = opts.baseDelay ?? 100;
    this.maxDelay = opts.maxDelay ?? 30000;
    this.enableCompression = opts.enableCompression ?? true;
    this.ndjson = opts.ndjson ?? false;
    this.batchSize = opts.batchSize || 50;
    this.flushIntervalMs = opts.flushIntervalMs || 5000;
    this.statsHandler = opts.statsHandler || null;
  }

  name() { return 'httpbatch'; }

  /** Get the last parsed collector response. */
  get lastCollectorResponse(): CollectorResponse | null { return this._lastResponse; }

  async write(encoded: string): Promise<void> {
    if (this.closed) throw new Error('httpbatch sink is closed');
    this.buffer.push(encoded);
    if (this.buffer.length >= this.batchSize) {
      await this.flush();
    } else {
      this.scheduleFlush();
    }
  }

  flush(): Promise<void> {
    if (this.flushTimer) {
      clearTimeout(this.flushTimer);
      this.flushTimer = null;
    }
    const operation = this.flushTail.then(() => this.flushOnce());
    this.flushTail = operation.catch(() => undefined);
    return operation;
  }

  private async flushOnce(): Promise<void> {
    if (this.buffer.length === 0) return;

    const events = this.buffer;
    this.buffer = [];
    try {
      const envelope = this.ndjson
        ? events.join('\n')
        : JSON.stringify({
            api_version: 'v1',
            source: { sdk: this.sdkName, version: this.sdkVersion, service: this.service },
            events: events.map(e => JSON.parse(e)),
          });

      const body = this.enableCompression
        ? await gzipAsync(Buffer.from(envelope, 'utf-8'))
        : Buffer.from(envelope, 'utf-8');

      let lastError: Error | null = null;
      for (let attempt = 0; attempt <= this.retries; attempt++) {
        try {
          const result = await this.post(body);
          this._lastResponse = result.response;
          this.notifyCollectorAck(result.response);

          const outcome = this.classifyOutcome(result.statusCode, result.response);
          if (outcome === 'success') return;

          const retryAfterMs = result.response.retry_after_ms
            ?? this.parseRetryAfter(result.retryAfterHeader);

          lastError = new Error(
            `collector reported ${outcome === 'retryable' ? 'retryable errors' : 'batch failure'}: ` +
            `${result.response.error || result.response.reason || `accepted=${result.response.accepted} rejected=${result.response.rejected}`}`
          );

          if (outcome === 'retryable' && attempt < this.retries) {
            const delay = retryAfterMs
              ?? Math.min(this.baseDelay * 2 ** attempt, this.maxDelay);
            await sleep(delay);
            continue;
          }
          throw lastError;
        } catch (err) {
          if (err instanceof Error && err.message.startsWith('collector reported')) throw err;
          lastError = err instanceof Error ? err : new Error(String(err));
          if (attempt < this.retries) {
            await sleep(Math.min(this.baseDelay * 2 ** attempt, this.maxDelay));
            continue;
          }
          throw new Error(`collector send failed: ${lastError.message}`);
        }
      }
      if (lastError) throw new Error(`collector send failed: ${lastError.message}`);
    } catch (error) {
      this.buffer = [...events, ...this.buffer];
      throw error;
    }
  }

  async drain(): Promise<void> {
    this.closed = true;
    this.pause();
    await this.flush();
  }
  pause(): void {
    if (this.flushTimer) {
      clearTimeout(this.flushTimer);
      this.flushTimer = null;
    }
  }
  resume(): void {
    if (!this.closed) this.scheduleFlush();
  }
  queueSize(): number {
    return this.buffer.length;
  }

  async close(): Promise<void> {
    await this.drain();
  }

  private scheduleFlush(): void {
    if (this.closed || this.buffer.length === 0 || this.flushTimer) return;
    this.flushTimer = setTimeout(() => {
      this.flushTimer = null;
      void this.flush().catch(error => {
        try {
          (this.statsHandler as DeliveryFailureHandler | null)?.onDeliveryFailed?.(undefined, error);
        } catch { /* stats callbacks cannot break delivery */ }
      });
    }, this.flushIntervalMs);
  }

  private classifyOutcome(statusCode: number, response: CollectorResponse): 'success' | 'retryable' | 'permanent' {
    // Check response-level retryable errors
    if (response.acks?.some(a => a.retryable)) return 'retryable';
    if (response.errors?.some(e => e.retryable)) return 'retryable';

    // HTTP status-based classification
    if (statusCode === 429 || statusCode === 503) return 'retryable';
    if (statusCode >= 300) return 'permanent';

    // Response status-based
    if (response.status === 'rejected' || response.status === 'invalid') {
      return response.accepted > 0 ? 'retryable' : 'permanent';
    }

    return 'success';
  }

  private parseRetryAfter(header: string | null | undefined): number | null {
    if (!header) return null;
    const trimmed = header.trim();
    if (!trimmed) return null;

    // Try numeric seconds
    const num = Number(trimmed);
    if (!isNaN(num)) return num * 1000;

    // Try HTTP-date
    try {
      const date = new Date(trimmed);
      if (!isNaN(date.getTime())) {
        const ms = date.getTime() - Date.now();
        return ms > 0 ? ms : 0;
      }
    } catch { /* ignore */ }

    return null;
  }

  private notifyCollectorAck(response: CollectorResponse): void {
    if (!this.statsHandler?.onCollectorAck) return;
    try {
      this.statsHandler.onCollectorAck({
        acks: response.acks || [],
        errors: response.errors || [],
        requestId: response.request_id || '',
        deduped: response.deduped || 0,
      });
    } catch { /* swallow */ }
  }

  private post(body: Buffer): Promise<{ statusCode: number; retryAfterHeader: string | null; response: CollectorResponse }> {
    return new Promise((resolve, reject) => {
      const url = new URL(this.endpoint);
      const isHttps = url.protocol === 'https:';
      const mod = isHttps ? https : http;

      const headers: Record<string, string> = {
        'Content-Type': this.ndjson ? 'application/x-ndjson' : 'application/json',
        'Content-Length': body.length.toString(),
        ...(this.enableCompression ? { 'Content-Encoding': 'gzip' } : {}),
      };
      if (this.apiKey) {
        headers[this.authHeader] = this.authHeader.toLowerCase() === 'authorization'
          ? `Bearer ${this.apiKey}`
          : this.apiKey;
      } else if (this.username && (this.password || isPublicDSNUsername(this.username))) {
        headers.Authorization = `Basic ${Buffer.from(`${this.username}:${this.password}`, 'utf8').toString('base64')}`;
      }

      const req = mod.request({
        hostname: url.hostname,
        port: url.port || (isHttps ? 443 : 80),
        path: url.pathname + url.search,
        method: 'POST',
        headers,
        timeout: this.timeout,
      }, (res) => {
        let data = '';
        res.on('data', (chunk: Buffer) => { data += chunk.toString(); });
        res.on('end', () => {
          const statusCode = res.statusCode || 0;
          const retryAfterHeader = res.headers['retry-after'] || null;

          let response: CollectorResponse;
          try {
            response = parseCollectorResponse(data);
          } catch {
            // If response isn't valid JSON, create a minimal response
            response = {
              request_id: '',
              status: statusCode >= 200 && statusCode < 300 ? 'accepted' : 'rejected',
              accepted: statusCode >= 200 && statusCode < 300 ? 1 : 0,
              rejected: statusCode >= 200 && statusCode < 300 ? 0 : 1,
              invalid: 0,
              acks: [],
            };
          }

          resolve({ statusCode, retryAfterHeader, response });
        });
      });

      req.on('error', reject);
      req.on('timeout', () => { req.destroy(); reject(new Error('timeout')); });
      req.write(body);
      req.end();
    });
  }
}

export class CollectorSink extends HTTPBatchSink {}

function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}

/** Options for HTTPBatchSink constructor. */
export interface HTTPBatchSinkOptions {
  endpoint: string;
  apiKey?: string;
  username?: string;
  password?: string;
  authHeader?: string;
  sdkVersion?: string;
  service?: string;
  timeout?: number;
  retries?: number;
  baseDelay?: number;
  maxDelay?: number;
  enableCompression?: boolean;
  ndjson?: boolean;
  batchSize?: number;
  flushIntervalMs?: number;
  statsHandler?: StatsHandler;
}

// --- MultiSink — fans out to multiple sinks ---

export class MultiSink implements Sink {
  private sinks: Sink[];
  constructor(sinks: Sink[]) { this.sinks = sinks; }
  name() { return 'multi'; }
  async write(encoded: string): Promise<void> {
    await Promise.all(this.sinks.map(s => Promise.resolve(s.write(encoded))));
  }
  async flush(): Promise<void> {
    await Promise.all(this.sinks.map(s => Promise.resolve(s.flush())));
  }
  async close(): Promise<void> {
    await Promise.all(this.sinks.map(s => Promise.resolve(s.close())));
  }
}

// --- OTLP Sink ---

export class OtlpSink implements Sink {
  private delegate: HTTPBatchSink;
  constructor(endpoint?: string) {
    this.delegate = new HTTPBatchSink({
      endpoint: endpoint || 'http://127.0.0.1:9308/events',
    });
  }
  name() { return 'otlp'; }
  async write(encoded: string): Promise<void> { await this.delegate.write(encoded); }
  async flush(): Promise<void> { await this.delegate.flush(); }
  async close(): Promise<void> { await this.delegate.close(); }
}

// --- Lowercase factory functions ---

export function stdoutSink(): StdoutSink { return new StdoutSink(); }
export function stderrSink(): StderrSink { return new StderrSink(); }
export function noopSink(): NoopSink { return new NoopSink(); }
export function memorySink(): MemorySink { return new MemorySink(); }
export function fileSink(path: string): FileSink { return new FileSink(path); }
export function rotatingFileSink(path: string, maxBytes?: number, maxBackups?: number): RotatingFileSink { return new RotatingFileSink(path, maxBytes, maxBackups); }
export function collectorSink(opts: HTTPBatchSinkOptions): CollectorSink { return new CollectorSink(opts); }
export function httpBatchSink(opts: HTTPBatchSinkOptions): HTTPBatchSink { return new HTTPBatchSink(opts); }
export function multiSink(...sinks: Sink[]): MultiSink { return new MultiSink(sinks); }
export function otlpSink(endpoint?: string): OtlpSink { return new OtlpSink(endpoint); }

export async function drain(sink: Sink & { drain?: () => void | Promise<void> }): Promise<void> {
  if (typeof sink.drain === 'function') return sink.drain();
  await Promise.resolve(sink.flush?.());
}

export function pause(sink: Sink & { pause?: () => void }): void {
  sink.pause?.();
}

export function resume(sink: Sink & { resume?: () => void }): void {
  sink.resume?.();
}

export function queueSize(sink: Sink & { queueSize?: () => number; getLength?: () => number }): number {
  if (typeof sink.queueSize === 'function') return sink.queueSize();
  if (typeof sink.getLength === 'function') return sink.getLength();
  return 0;
}

export async function health(sink: Sink): Promise<{ name: string; status: 'healthy' | 'unhealthy' }> {
  const healthy = typeof sink.health === 'function' ? await sink.health() : true;
  return { name: sink.name(), status: healthy ? 'healthy' : 'unhealthy' };
}

/** Options accepted by {@link kafkaSink}. */
export interface KafkaSinkOptions {
  endpoint?: string;
  apiKey?: string;
  username?: string;
  password?: string;
  authHeader?: string;
  sdkName?: string;
  sdkVersion?: string;
  service?: string;
  topic?: string;
  timeout?: number;
  retries?: number;
  baseDelay?: number;
  maxDelay?: number;
  enableCompression?: boolean;
  ndjson?: boolean;
  batchSize?: number;
  flushIntervalMs?: number;
  statsHandler?: StatsHandler;
}

const KAFKA_SINK_OPTION_KEYS = new Set<keyof KafkaSinkOptions>([
  'endpoint', 'apiKey', 'username', 'password', 'authHeader', 'sdkName',
  'sdkVersion', 'service', 'topic', 'timeout', 'retries', 'baseDelay',
  'maxDelay', 'enableCompression', 'ndjson', 'batchSize', 'flushIntervalMs',
  'statsHandler',
]);

/**
 * Send events to the collector with Kafka routing metadata.
 *
 * This is a thin wrapper over HTTPBatchSink: the collector must have a Kafka
 * sink configured to route events by the `topic`/`service` metadata. All HTTP
 * batch options (credentials, timeouts, retries, ...) are forwarded to the
 * underlying sink, and `topic` is used as the routing `service` (matching the
 * Python SDK's `kafka_sink`). Unsupported keys are rejected rather than
 * silently discarded.
 */
export function kafkaSink(config?: Record<string, unknown>): Sink {
  const raw = config ?? {};
  const unsupported = Object.keys(raw)
    .filter(key => !KAFKA_SINK_OPTION_KEYS.has(key as keyof KafkaSinkOptions))
    .sort();
  if (unsupported.length > 0) {
    throw new Error(
      `invalid kafka sink options: unsupported key(s): ${unsupported.join(', ')}. ` +
      `Supported keys: ${[...KAFKA_SINK_OPTION_KEYS].sort().join(', ')}`
    );
  }

  const endpoint = (raw.endpoint as string) || 'http://127.0.0.1:9308/events';
  const topic = raw.topic as string | undefined;
  const service = topic || (raw.service as string | undefined) || '';

  return new HTTPBatchSink({
    endpoint,
    service,
    apiKey: raw.apiKey as string | undefined,
    username: raw.username as string | undefined,
    password: raw.password as string | undefined,
    authHeader: raw.authHeader as string | undefined,
    sdkName: raw.sdkName as string | undefined,
    sdkVersion: raw.sdkVersion as string | undefined,
    timeout: raw.timeout as number | undefined,
    retries: raw.retries as number | undefined,
    baseDelay: raw.baseDelay as number | undefined,
    maxDelay: raw.maxDelay as number | undefined,
    enableCompression: raw.enableCompression as boolean | undefined,
    ndjson: raw.ndjson as boolean | undefined,
    batchSize: raw.batchSize as number | undefined,
    flushIntervalMs: raw.flushIntervalMs as number | undefined,
    statsHandler: raw.statsHandler as StatsHandler | undefined,
  });
}

export function httpSink(opts: HTTPBatchSinkOptions): HTTPBatchSink {
  return httpBatchSink(opts);
}
