import { Logger } from '../core/logger.ts';
import { String as AttrString, Int as AttrInt } from '../core/event.ts';
import { runWithEvent } from '../core/context.ts';

export interface NextMiddlewareOptions {
  service?: string;
}

/** Validate an IPv4 literal without relying on Node's `node:net` (unavailable in Edge). */
function isIPv4(value: string): boolean {
  const octets = value.split('.');
  if (octets.length !== 4) return false;
  for (const octet of octets) {
    if (!/^\d{1,3}$/.test(octet)) return false;
    if (Number(octet) > 255) return false;
  }
  return true;
}

/** Validate an IPv6 literal, including IPv4-mapped forms such as ::ffff:1.2.3.4. */
function isIPv6(value: string): boolean {
  if (value.includes('%')) return false; // zone identifiers are not IP literals
  if (!/^[0-9a-fA-F:.]+$/.test(value)) return false;
  const halves = value.split('::');
  if (halves.length > 2) return false;
  const groups = [
    ...(halves[0] ? halves[0].split(':') : []),
    ...(halves.length === 2 && halves[1] ? halves[1].split(':') : []),
  ];
  let slots = 0;
  for (const group of groups) {
    if (group.includes('.')) {
      if (!isIPv4(group)) return false;
      slots += 2;
    } else {
      if (!/^[0-9a-fA-F]{1,4}$/.test(group)) return false;
      slots += 1;
    }
  }
  // A single "::" compresses at least one 16-bit group.
  return halves.length === 2 ? slots < 8 : slots === 8;
}

function isIP(value: string): boolean {
  return isIPv4(value) || isIPv6(value);
}

/**
 * Pick the remote IP from an X-Forwarded-For chain.
 *
 * X-Forwarded-For is client-controllable: a caller can prepend arbitrary
 * entries, so the leftmost value (and the verbatim header) cannot be trusted.
 * Proxies append the address they observed to the right, so the rightmost
 * valid IP is the closest hop the trusted infrastructure actually saw. Invalid
 * entries are skipped. Returns '' when the header holds no usable IP.
 */
function remoteIpFromForwardedFor(value: string | null | undefined): string {
  if (!value) return '';
  const parts = value.split(',');
  for (let i = parts.length - 1; i >= 0; i--) {
    const candidate = parts[i].trim();
    if (candidate && isIP(candidate)) return candidate;
  }
  return '';
}

export function lozaNextMiddleware(opts: NextMiddlewareOptions = {}) {
  const loza = new Logger({ service: opts.service });

  return async (request: any, event: any) => {
    const startedAt = Date.now();
    const route = request.nextUrl?.pathname || request.url || '';

    const ev = loza.startHTTPEvent({
      event: `${request.method || 'GET'} ${route}`,
      kind: 'http',
      method: request.method || 'GET',
      path: route,
      route,
      service: opts.service,
    });

    loza.enrich(ev,
      AttrString('http.user_agent', request.headers?.get('user-agent') || ''),
      AttrString('http.remote_ip', remoteIpFromForwardedFor(request.headers?.get('x-forwarded-for'))),
    );

    try {
      // Scope downstream handling with run(); context is restored afterwards.
      const response = await runWithEvent(ev, () => event());
      const durationMs = Date.now() - startedAt;
      const outcome = response.status >= 500 ? 'error' : 'success';
      loza.finish(ev, outcome,
        AttrInt('status_code', response.status),
        AttrInt('duration_ms', durationMs),
      );
      await loza.emit(ev).catch(() => {});
      return response;
    } catch (err: any) {
      loza.finishError(ev, err);
      await loza.emit(ev).catch(() => {});
      throw err;
    }
  };
}
