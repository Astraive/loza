/** Timing primitives: Process, Timer, Group, Stopwatch. */

export interface ProcessEntry {
  step: number;
  name: string;
  started_at_ms: number;
  ended_at_ms: number;
  duration_ms: number;
  status_code?: number;
  attrs?: Record<string, any>;
}

export interface GroupEntry {
  name: string;
  started_at_ms: number;
  ended_at_ms: number;
  duration_ms: number;
  status_code?: number;
  attrs?: Record<string, any>;
}

export interface TimerEntry {
  name: string;
  duration_ms: number;
  status_code?: number;
  attrs?: Record<string, any>;
}

/**
 * Split timing attrs into a numeric status code and the remaining attributes.
 * Shared by process/group `finish` and timer `stop` so the parsing stays consistent.
 */
function extractStatusCode(attrs: any[]): { code: number; extra: Record<string, any> } {
  let code = 0;
  const extra: Record<string, any> = {};
  for (const a of attrs) {
    if (a && a.key) {
      if (a.key === 'status_code' && typeof a.value === 'number') {
        code = a.value;
      } else {
        extra[a.key] = a.value;
      }
    }
  }
  return { code, extra };
}

/** Collect Attr-shaped objects into entry attrs and an optional status code. */
function collectAttrs(attrs: any[]): { extra: Record<string, any>; statusCode: number } {
  const extra: Record<string, any> = {};
  let statusCode = 0;
  for (const a of attrs) {
    if (a && a.key) {
      if (a.key === 'status_code' && typeof a.value === 'number') {
        statusCode = a.value;
      } else {
        extra[a.key] = a.value;
      }
    }
  }
  return { extra, statusCode };
}

/** Handle for a named process step with automatic duration tracking. */
export class ProcessHandle {
  private _event: any;
  private _name: string;
  private _step: number;
  private _startedAt: number;
  private _startedAtMs: number;
  private _attrs: any[];

  constructor(event: any, name: string, step: number, startedAt: number, attrs: any[] = []) {
    this._event = event;
    this._name = name;
    this._step = step;
    this._startedAt = startedAt;
    this._startedAtMs = startedAt - (this._event.startedAt || startedAt);
    this._attrs = attrs;
  }

  finish(...attrs: any[]): void {
    const now = Date.now();
    const endedMs = now - (this._event.startedAt || now);
    const { extra, statusCode } = collectAttrs([...this._attrs, ...attrs]);
    const { code: statusCode, extra } = extractStatusCode(attrs);
    const entry: ProcessEntry = {
      step: this._step,
      name: this._name,
      started_at_ms: this._startedAtMs,
      ended_at_ms: endedMs,
      duration_ms: endedMs - this._startedAtMs,
    };
    if (statusCode) entry.status_code = statusCode;
    if (Object.keys(extra).length > 0) entry.attrs = extra;
    if (!this._event.processes) this._event.processes = [];
    this._event.processes.push(entry);
  }

  finishError(err: unknown, ...attrs: any[]): void {
    this.finish({ key: 'error_message', value: String(err) }, ...attrs);
  }

  duration(): number {
    return Date.now() - this._startedAt;
  }
}

/** Handle for a named timer with automatic duration tracking. */
export class TimerHandle {
  private _event: any;
  private _name: string;
  private _startedAt: number;
  private _attrs: any[];

  constructor(event: any, name: string, startedAt: number, attrs: any[] = []) {
    this._event = event;
    this._name = name;
    this._startedAt = startedAt;
    this._attrs = attrs;
  }

  stop(...attrs: any[]): void {
    const durationMs = Date.now() - this._startedAt;
    const { extra, statusCode } = collectAttrs([...this._attrs, ...attrs]);
    const { code: statusCode, extra } = extractStatusCode(attrs);
    const entry: TimerEntry = {
      name: this._name,
      duration_ms: durationMs,
    };
    if (statusCode) entry.status_code = statusCode;
    if (Object.keys(extra).length > 0) entry.attrs = extra;
    if (!this._event.timers) this._event.timers = [];
    this._event.timers.push(entry);
  }
  stopTimer(...attrs: any[]): void {
    this.stop(...attrs);
  }

  stopError(err: unknown, ...attrs: any[]): void {
    this.stop({ key: 'error_message', value: String(err) }, ...attrs);
  }

  duration(): number {
    return Date.now() - this._startedAt;
  }
}

/** withProcess wraps fn inside a named process step. */
export function withProcess(event: any, name: string, fn: () => void, ...attrs: any[]): void {
  const h = event.startProcess(name, ...attrs);
  try { fn(); h.finish(); } catch (e) { h.finishError(e); throw e; }
}

/** withGroup wraps fn inside a named group phase. */
export function withGroup(event: any, name: string, fn: () => void, ...attrs: any[]): void {
  const h = event.startGroup(name, ...attrs);
  try { fn(); h.finish(); } catch (e) { h.finishError(e); throw e; }
}

/** withTimer wraps fn inside a named timer. */
export function withTimer(event: any, name: string, fn: () => void, ...attrs: any[]): void {
  const h = event.startTimer(name, ...attrs);
  try { fn(); h.stop(); } catch (e) { h.stopError(e); throw e; }
}

/** Handle for a named group phase with automatic duration tracking. */
export class GroupHandle {
  private _event: any;
  private _name: string;
  private _startedAt: number;
  private _startedAtMs: number;
  private _attrs: any[];

  constructor(event: any, name: string, startedAt: number, attrs: any[] = []) {
    this._event = event;
    this._name = name;
    this._startedAt = startedAt;
    this._startedAtMs = startedAt - (this._event.startedAt || startedAt);
    this._attrs = attrs;
  }

  finish(...attrs: any[]): void {
    const now = Date.now();
    const endedMs = now - (this._event.startedAt || now);
    const { extra, statusCode } = collectAttrs([...this._attrs, ...attrs]);
    const { code: statusCode, extra } = extractStatusCode(attrs);
    const entry: GroupEntry = {
      name: this._name,
      started_at_ms: this._startedAtMs,
      ended_at_ms: endedMs,
      duration_ms: endedMs - this._startedAtMs,
    };
    if (statusCode) entry.status_code = statusCode;
    if (Object.keys(extra).length > 0) entry.attrs = extra;
    if (!this._event.groups) this._event.groups = [];
    this._event.groups.push(entry);
  }

  finishError(err: unknown, ...attrs: any[]): void {
    this.finish({ key: 'error_message', value: String(err) }, ...attrs);
  }

  finishGroupError(err: unknown, ...attrs: any[]): void {
    this.finishError(err, ...attrs);
  }

  duration(): number {
    return Date.now() - this._startedAt;
  }
}

/** Measure creates a standalone stopwatch. */
export function measure(): StopwatchHandle { return new StopwatchHandle(); }

/** Phase is a convenience alias for starting a group on an event. */
export function phase(event: any, name: string, ...attrs: any[]): GroupHandle {
  return event.startGroup(name, ...attrs);
}

/** Span is a convenience alias for starting a timer on an event. */
export function span(event: any, name: string, ...attrs: any[]): TimerHandle {
  return event.startTimer(name, ...attrs);
}

/** Step is a convenience alias for starting a process on an event. */
export function step(event: any, name: string, ...attrs: any[]): ProcessHandle {
  return event.startProcess(name, ...attrs);
}

/** Standalone elapsed-time measurer with no event reference. */
export class StopwatchHandle {
  private _startedAt: number;

  constructor() {
    this._startedAt = Date.now();
  }

  elapsed(): number {
    return Date.now() - this._startedAt;
  }
}

/** Create a new standalone stopwatch. */
export function stopwatch(): StopwatchHandle {
  return new StopwatchHandle();
}
