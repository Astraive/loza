import type { LozaDSN } from './dsn.ts';

/** Mutable config fields that a loza:// DSN resolves. */
export type DSNConfigTarget = {
  collectorUrl: string;
  collectorName: string;
  username: string;
  password: string;
  environment: string;
  service: string;
};

/**
 * Apply a parsed loza:// DSN to a config object.
 *
 * This is the single source of truth for DSN-to-Config mapping: every path
 * (LOZA_DSN, code-level collectorUrl and YAML collector_url) resolves the
 * endpoint, collector name and credentials, then derives environment/service
 * using identical rules.
 *
 * Callers apply their own explicit environment/service values *after* this
 * function, so an explicitly configured value always wins over a DSN-derived
 * one within the same config layer.
 */
export function applyDSN(cfg: DSNConfigTarget, dsn: LozaDSN): void {
  cfg.collectorUrl = dsn.baseURL;
  cfg.collectorName = dsn.collectorName;
  if (dsn.username !== undefined) {
    cfg.username = dsn.username;
    cfg.password = dsn.password ?? '';
  }
  if (dsn.env && dsn.env !== 'default') cfg.environment = dsn.env;
  if (dsn.service) cfg.service = dsn.service;
}
