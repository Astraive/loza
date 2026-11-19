import { randomBytes } from 'node:crypto';

let customIdGenerator: (() => string) | null = null;

export function setUUIDGenerator(fn?: (() => string) | null): void {
  customIdGenerator = fn ?? null;
}

export function resetUUIDGenerator(): void {
  customIdGenerator = null;
}

// Monotonic state (RFC 9562 section 6.2, "Monotonic Random").
// The 74 random bits (12-bit rand_a + 62-bit rand_b) are treated as a single
// counter, so IDs generated within one millisecond sort in creation order.
const RANDOM_BITS = 74n;
const RANDOM_MASK = (1n << RANDOM_BITS) - 1n;
const RAND_B_BITS = 62n;
const RAND_B_MASK = (1n << RAND_B_BITS) - 1n;

let lastMs = -1;
let seq = 0n;

/** Seed a fresh 74-bit random sequence (10 bytes = 80 bits, masked to 74). */
function randomSequence(): bigint {
  let value = 0n;
  for (const byte of randomBytes(10)) {
    value = (value << 8n) | BigInt(byte);
  }
  return value & RANDOM_MASK;
}

/**
 * Generate a UUIDv7 string (monotonic, sortable by time).
 *
 * The 48-bit timestamp gives millisecond ordering. Within the same
 * millisecond a per-process counter is incremented so IDs are strictly
 * increasing in creation order. If the system clock moves backwards, the
 * previous timestamp is reused until the clock catches up, keeping ordering
 * intact (RFC 9562 monotonic variant).
 * Format: xxxxxxxx-xxxx-7xxx-yxxx-xxxxxxxxxxxx
 */
export function uuidv7(): string {
  if (customIdGenerator) {
    return customIdGenerator();
  }

  const now = Date.now();
  if (now > lastMs) {
    // New millisecond: reseed with fresh randomness.
    lastMs = now;
    seq = randomSequence();
  } else {
    // Same millisecond or clock rollback: freeze the timestamp and advance
    // the counter so successive IDs remain strictly increasing.
    seq = (seq + 1n) & RANDOM_MASK;
  }

  const bytes = Buffer.alloc(16);

  // 48-bit timestamp (ms since epoch) in first 6 bytes.
  const ts = BigInt(lastMs);
  bytes[0] = Number((ts >> 40n) & 0xffn);
  bytes[1] = Number((ts >> 32n) & 0xffn);
  bytes[2] = Number((ts >> 24n) & 0xffn);
  bytes[3] = Number((ts >> 16n) & 0xffn);
  bytes[4] = Number((ts >> 8n) & 0xffn);
  bytes[5] = Number(ts & 0xffn);

  // Split the 74-bit counter into 12-bit rand_a and 62-bit rand_b.
  const randA = Number((seq >> RAND_B_BITS) & 0xfffn);
  const randB = seq & RAND_B_MASK;

  // Version 7 (rand_a occupies byte 6 low nibble and all of byte 7).
  bytes[6] = 0x70 | ((randA >> 8) & 0x0f);
  bytes[7] = randA & 0xff;
  // Variant 10 (rand_b occupies byte 8 low 6 bits and bytes 9-15).
  bytes[8] = 0x80 | Number((randB >> 56n) & 0x3fn);
  bytes[9] = Number((randB >> 48n) & 0xffn);
  bytes[10] = Number((randB >> 40n) & 0xffn);
  bytes[11] = Number((randB >> 32n) & 0xffn);
  bytes[12] = Number((randB >> 24n) & 0xffn);
  bytes[13] = Number((randB >> 16n) & 0xffn);
  bytes[14] = Number((randB >> 8n) & 0xffn);
  bytes[15] = Number(randB & 0xffn);

  const hex = bytes.toString('hex');
  return [
    hex.slice(0, 8),
    hex.slice(8, 12),
    hex.slice(12, 16),
    hex.slice(16, 20),
    hex.slice(20, 32),
  ].join('-');
}
