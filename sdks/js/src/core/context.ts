import { AsyncLocalStorage } from 'node:async_hooks';
import { Event } from './event.ts';

const storage = new AsyncLocalStorage<Event>();

/** Get the event from the current async context, or undefined. */
export function getEvent(): Event | undefined {
  return storage.getStore();
}

/** Check if there's an event in the current context. */
export function hasEvent(): boolean {
  return storage.getStore() !== undefined;
}

/** Get the event ID from the current context. */
export function eventId(): string {
  return storage.getStore()?.eventId ?? '';
}

/**
 * Run a function with a specific event in context, scoped to the callback.
 *
 * The context is installed with `AsyncLocalStorage.run`, so it is removed as
 * soon as the callback (and any async work created inside it) completes, and
 * the previous context is restored. This is the only supported way to set
 * event context: unlike `enterWith`, `run` cannot leak into unrelated work in
 * long-lived async chains (servers, workers, connection handlers).
 */
export function runWithEvent<T>(event: Event, fn: () => T): T {
  return storage.run(event, fn);
}

/**
 * Run a function with no event in context, scoped to the callback.
 *
 * Equivalent to `AsyncLocalStorage.exit(fn)`: the previous context is restored
 * afterwards. Use this to deliberately isolate work from the current event.
 */
export function withoutEvent<T>(fn: () => T): T {
  return storage.run(undefined, fn);
}

/** Get the request ID from the current context. */
export function requestIdFromContext(): string {
  return storage.getStore()?.requestId ?? '';
}

/** Get the trace ID from the current context. */
export function traceIdFromContext(): string {
  return storage.getStore()?.traceId ?? '';
}
