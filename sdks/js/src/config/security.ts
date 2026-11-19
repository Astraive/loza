export interface SecurityConfig {
  redactByDefault?: boolean;
  allowPII?: boolean;
  maxFieldBytes?: number;
  maxEventBytes?: number;
  maxAttrCount?: number;
  dropOversizedEvents?: boolean;
}

const DEFAULT_SECURITY: Required<SecurityConfig> = {
  redactByDefault: false,
  allowPII: false,
  maxFieldBytes: 4096,
  maxEventBytes: 262144,
  maxAttrCount: 512,
  dropOversizedEvents: true,
};

export class SecurityLimiter {
  private config: Required<SecurityConfig>;

  constructor(config?: SecurityConfig) {
    this.config = { ...DEFAULT_SECURITY, ...config };
  }

  /** Returns true if the event should be dropped. */
  shouldDrop(encoded: string, attrCount: number): boolean {
    if (!this.config.dropOversizedEvents) return false;
    // `.length` counts UTF-16 code units, not UTF-8 bytes. A multibyte
    // character can be up to ~3x (BMP) or ~2x (astral) its code-unit length,
    // so measuring bytes is required to actually enforce the byte cap.
    if (Buffer.byteLength(encoded, 'utf8') > this.config.maxEventBytes) return true;
    if (attrCount > this.config.maxAttrCount) return true;
    return false;
  }

  /** Check if a single field value exceeds max field bytes. */
  isFieldOversized(value: string): boolean {
    return Buffer.byteLength(value, 'utf8') > this.config.maxFieldBytes;
  }

  getConfig(): Required<SecurityConfig> {
    return { ...this.config };
  }
}
