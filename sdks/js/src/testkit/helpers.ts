import { Logger } from '../core/logger.ts';
import { MemorySink } from '../sinks/standard-sinks.ts';
import type { Event, Attr, Params } from '../core/event.ts';
import { resetUUIDGenerator, setUUIDGenerator } from '../core/uuidv7.ts';
import { setClock as _setClock, resetClock } from '../core/event.ts';
import fs from 'node:fs';

export interface TestLoggerResult {
  logger: Logger;
  sink: MemorySink;
}

/** Creates a logger configured for tests plus its backing memory sink. */
export function testLogger(config?: Partial<Params>): TestLoggerResult {
  const sink = new MemorySink();
  const logger = new Logger({
    service: config?.service || 'test',
    environment: 'test',
    sink,
  });
  return { logger, sink };
}

/** Runs fn with a temporary memory sink and returns captured event JSON strings. */
export async function capture(fn: (logger: Logger) => void | Promise<void>): Promise<string[]> {
  const sink = new MemorySink();
  const logger = new Logger({
    service: 'test',
    environment: 'test',
    sink,
  });
  await fn(logger);
  await logger.flush();
  return sink.getEvents();
}

/**
 * Buffer behind the module-level `events()` / `lastEvent()` / `clearEvents()`
 * helpers. It follows the most recently created testkit so the shared free
 * functions keep reflecting the active kit for backwards compatibility, while
 * each kit's own accessors stay bound to its private buffer.
 */
let activeCapturedEvents: string[] = [];

/** Build capture accessors bound to one specific buffer. */
function bindCapture(store: string[]): {
  events: () => string[];
  lastEvent: () => string | undefined;
  clearEvents: () => void;
} {
  return {
    events: () => [...store],
    lastEvent: () => store[store.length - 1],
    clearEvents: () => { store.length = 0; },
  };
}

export function testkit(): TestLoggerResult & {
  capture: typeof capture;
  events: () => string[];
  lastEvent: () => string | undefined;
  clearEvents: () => void;
} {
  const kit = testLogger();
  // Per-instance buffer: a kit captures only its own sink's events, so two
  // independent/parallel testkits can never bleed into one another.
  const capturedEvents: string[] = [];
  const originalWrite = kit.sink.write.bind(kit.sink);
  kit.sink.write = (encoded: string) => {
    capturedEvents.push(encoded);
    return originalWrite(encoded);
  };
  activeCapturedEvents = capturedEvents;
  return {
    ...kit,
    capture,
    ...bindCapture(capturedEvents),
  };
}

export function events(): string[] {
  return [...activeCapturedEvents];
}

export function lastEvent(): string | undefined {
  return activeCapturedEvents[activeCapturedEvents.length - 1];
}

export function clearEvents(): void {
  activeCapturedEvents.length = 0;
}

/** Asserts that a decoded event JSON contains the expected key-value pair. */
export function assertEvent(eventJson: string, key: string, expected: unknown): void {
  const parsed = JSON.parse(eventJson);
  const actual = getNestedValue(parsed, key);
  if (actual === undefined) {
    throw new Error(`assertEvent: key "${key}" not found in event`);
  }
  if (JSON.stringify(actual) !== JSON.stringify(expected)) {
    throw new Error(`assertEvent: key "${key}" expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
  }
}

/** Asserts that a decoded event JSON has a specific attribute value. */
export function assertAttr(eventJson: string, key: string, expected: unknown): void {
  assertEvent(eventJson, key, expected);
}

/** Alias for assertEvent. */
export const expectEvent = assertEvent;
/** Alias for assertAttr. */
export const expectAttr = assertAttr;

/** Asserts that a field is redacted. */
export function assertRedacted(eventJson: string, key: string): void {
  const parsed = JSON.parse(eventJson);
  const actual = getNestedValue(parsed, key);
  if (actual !== '[REDACTED]') {
    throw new Error(`assertRedacted: key "${key}" expected "[REDACTED]", got ${JSON.stringify(actual)}`);
  }
}

/** Asserts that a checkpoint exists in the event. */
export function assertHasCheckpoint(eventJson: string, name: string): void {
  const parsed = JSON.parse(eventJson);
  const checkpoints = parsed.checkpoints || [];
  const found = checkpoints.some((cp: any) => cp.name === name);
  if (!found) {
    throw new Error(`assertHasCheckpoint: checkpoint "${name}" not found`);
  }
}

/** Expect that a decoded event JSON matches a snapshot string. */
export function snapshotEvent(eventJson: string, snapshot: string): void {
  const normalized = JSON.stringify(JSON.parse(eventJson));
  if (normalized !== snapshot) {
    throw new Error(`snapshotEvent: event does not match snapshot`);
  }
}

/** Mock sink for testing. */
export class MockSink {
  public events: string[] = [];
  public failNext = false;
  name() { return 'mock'; }
  async write(encoded: string): Promise<void> {
    if (this.failNext) { this.failNext = false; throw new Error('mock write failure'); }
    this.events.push(encoded);
  }
  flush() {}
  close() {}
  getEvents(): string[] { return [...this.events]; }
  clear(): void { this.events = []; }
}

/** Mock clock for deterministic timing. */
export class FakeClock {
  private _now: number;
  constructor(now: number = Date.now()) { this._now = now; }
  now(): number { return this._now; }
  advance(ms: number): void { this._now += ms; }
  setTime(ms: number): void { this._now = ms; }
}

export function setClock(clock: FakeClock | (() => number)): FakeClock | (() => number) {
  if (typeof clock === 'function') {
    _setClock(clock);
  } else if (clock && typeof (clock as FakeClock).now === 'function') {
    _setClock(() => (clock as FakeClock).now());
  } else {
    throw new Error('setClock: expected a function or an object with a now() method');
  }
  return clock;
}

/** Set a custom ID generator for deterministic tests. */
export function setIdGenerator(fn: () => string): void {
  setUUIDGenerator(fn);
}

export function resetForTest(): void {
  resetUUIDGenerator();
  resetClock();
  clearEvents();
}

export function goldenTest(path: string, eventJson: string): boolean {
  const actual = JSON.stringify(JSON.parse(eventJson));
  if (!fs.existsSync(path)) {
    fs.writeFileSync(path, actual + '\n');
    return true;
  }
  const expected = JSON.stringify(JSON.parse(fs.readFileSync(path, 'utf8')));
  return expected === actual;
}

export function conformanceSuite(): { name: string; status: 'available' } {
  return { name: 'loza-js-conformance', status: 'available' };
}

function getNestedValue(obj: any, path: string): any {
  const parts = path.split('.');
  let current = obj;
  for (const part of parts) {
    if (current == null || typeof current !== 'object') return undefined;
    current = current[part];
  }
  return current;
}
