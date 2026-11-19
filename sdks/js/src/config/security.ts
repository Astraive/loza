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

  constructor(config: SecurityConfig = {}) {
    // Merge field-by-field so an explicitly `undefined` value (e.g. from a
    // partially-built object) cannot overwrite a sane default and silently
    // disable a limit.
    this.config = {
      redactByDefault: config.redactByDefault ?? DEFAULT_SECURITY.redactByDefault,
      allowPII: config.allowPII ?? DEFAULT_SECURITY.allowPII,
      maxFieldBytes: config.maxFieldBytes ?? DEFAULT_SECURITY.maxFieldBytes,
      maxEventBytes: config.maxEventBytes ?? DEFAULT_SECURITY.maxEventBytes,
      maxAttrCount: config.maxAttrCount ?? DEFAULT_SECURITY.maxAttrCount,
      dropOversizedEvents: config.dropOversizedEvents ?? DEFAULT_SECURITY.dropOversizedEvents,
    };
  }

  /** Returns true if the event should be dropped. */
  shouldDrop(encoded: string, attrCount: number): boolean {
    if (!this.config.dropOversizedEvents) return false;
    if (encoded.length > this.config.maxEventBytes) return true;
    if (attrCount > this.config.maxAttrCount) return true;
    return false;
  }

  /** Check if a single field value exceeds max field bytes. */
  isFieldOversized(value: string): boolean {
    return value.length > this.config.maxFieldBytes;
  }

  getConfig(): Required<SecurityConfig> {
    return { ...this.config };
  }
}
