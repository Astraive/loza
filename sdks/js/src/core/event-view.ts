import type { Event } from './event.ts';
import type { Checkpoint } from './event.ts';
import type { ProcessEntry, GroupEntry, TimerEntry } from './timing.ts';
import type { ErrorInfo } from './errors.ts';
import type { EventState } from './event.ts';
import type { Level } from './level.ts';

/**
 * Produce a deeply frozen, structurally independent copy of a JSON-like value.
 *
 * The view is documented as immutable, but freezing only the outer container
 * leaves nested objects/arrays (and the `error` object) mutable, and freezing
 * in place would also freeze the source event's shared nested values. Cloning
 * as we freeze gives the view a true snapshot without mutating the event.
 * Cyclic references are handled via the `seen` map.
 */
function freezeClone<T>(value: T, seen: WeakMap<object, any> = new WeakMap()): T {
  if (value === null || typeof value !== 'object') return value;
  if (seen.has(value as object)) return seen.get(value as object) as T;
  // Preserve Date values instead of degrading them to plain objects.
  if (value instanceof Date) return Object.freeze(new Date(value.getTime())) as T;

  if (Array.isArray(value)) {
    const copy: any[] = [];
    seen.set(value as object, copy);
    for (const item of value as unknown as any[]) {
      copy.push(freezeClone(item, seen));
    }
    return Object.freeze(copy) as T;
  }

  const copy: Record<string, any> = {};
  seen.set(value as object, copy);
  for (const [key, item] of Object.entries(value as Record<string, any>)) {
    // defineProperty keeps a caller-controlled `__proto__` key as an ordinary
    // own property instead of invoking the inherited prototype setter.
    Object.defineProperty(copy, key, {
      value: freezeClone(item, seen),
      writable: true,
      enumerable: true,
      configurable: true,
    });
  }
  return Object.freeze(copy) as T;
}

/** Immutable read-only view of an Event, passed to Schema.encode(). */
export class EventView {
  readonly schemaVersion: string;
  readonly eventVersion: string;
  readonly eventId: string;
  readonly requestId: string;
  readonly traceId: string;
  readonly spanId: string;
  readonly incidentId: string;
  readonly parentId: string;
  readonly timestamp: string;
  readonly service: string;
  readonly event: string;
  readonly kind: string;
  readonly level: Level;
  readonly message: string;
  readonly outcome: string;
  readonly version: string;
  readonly environment: string;
  readonly deploymentId: string;
  readonly region: string;
  readonly host: string;
  readonly runtime: string;
  readonly method: string;
  readonly path: string;
  readonly route: string;
  readonly statusCode: number;
  readonly durationMs: number;
  readonly startedAt: number;
  readonly finishedAt: number;
  readonly attrs: Readonly<Record<string, any>>;
  readonly checkpoints: ReadonlyArray<Checkpoint>;
  readonly processes: ReadonlyArray<ProcessEntry>;
  readonly groups: ReadonlyArray<GroupEntry>;
  readonly timers: ReadonlyArray<TimerEntry>;
  readonly error: Readonly<ErrorInfo> | null;
  readonly state: EventState;
  readonly sensitiveKeys: ReadonlySet<string>;
  readonly hashKeys: ReadonlySet<string>;

  constructor(event: Event) {
    this.schemaVersion = event.schemaVersion;
    this.eventVersion = event.eventVersion;
    this.eventId = event.eventId;
    this.requestId = event.requestId;
    this.traceId = event.traceId;
    this.spanId = event.spanId;
    this.incidentId = event.incidentId;
    this.parentId = event.parentId;
    this.timestamp = event.timestamp;
    this.service = event.service;
    this.event = event.event;
    this.kind = event.kind;
    this.level = event.level;
    this.message = event.message;
    this.outcome = event.outcome;
    this.version = event.version;
    this.environment = event.environment;
    this.deploymentId = event.deploymentId;
    this.region = event.region;
    this.host = event.host;
    this.runtime = event.runtime;
    this.method = event.method;
    this.path = event.path;
    this.route = event.route;
    this.statusCode = event.statusCode;
    this.durationMs = event.durationMs;
    this.startedAt = event.startedAt;
    this.finishedAt = event.finishedAt;
    this.attrs = freezeClone(event.attrs ?? {});
    this.checkpoints = freezeClone(event.checkpoints ?? []);
    this.processes = freezeClone(event.processes ?? []);
    this.groups = freezeClone(event.groups ?? []);
    this.timers = freezeClone(event.timers ?? []);
    this.error = event.error ? freezeClone(event.error) : null;
    this.state = event.state;
    this.sensitiveKeys = new Set(event.sensitiveKeys);
    this.hashKeys = new Set(event.hashKeys);
  }
}
