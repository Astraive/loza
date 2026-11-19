import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { defaultConfig, withOptions } from '../src/config/config.ts';
import { mergeFileConfig } from '../src/config/config-file.ts';

const DSN = 'loza://localhost:9308/collector?env=staging&service=payments';

describe('DSN application parity', () => {
  it('derives the same environment/service from a DSN via YAML and via code', () => {
    const fromYAML = mergeFileConfig(defaultConfig(), { collector_url: DSN });
    const fromCode = withOptions(defaultConfig(), { collectorUrl: DSN });

    for (const cfg of [fromYAML, fromCode]) {
      assert.equal(cfg.collectorUrl, 'http://localhost:9308');
      assert.equal(cfg.collectorName, 'collector');
      assert.equal(cfg.environment, 'staging');
      assert.equal(cfg.service, 'payments');
    }
  });

  it('lets explicit YAML values override DSN-derived environment/service', () => {
    const cfg = mergeFileConfig(defaultConfig(), {
      collector_url: DSN,
      environment: 'production',
      service_name: 'api',
    });
    assert.equal(cfg.environment, 'production');
    assert.equal(cfg.service, 'api');
  });

  it('lets explicit code options override DSN-derived environment/service', () => {
    const cfg = withOptions(defaultConfig(), {
      collectorUrl: DSN,
      environment: 'production',
      service: 'api',
    });
    assert.equal(cfg.environment, 'production');
    assert.equal(cfg.service, 'api');
  });

  it('is identical for the same DSN plus explicit values in either path', () => {
    const fromYAML = mergeFileConfig(defaultConfig(), {
      collector_url: DSN,
      environment: 'production',
      service: 'api',
    });
    const fromCode = withOptions(defaultConfig(), {
      collectorUrl: DSN,
      environment: 'production',
      service: 'api',
    });
    assert.equal(fromYAML.environment, fromCode.environment);
    assert.equal(fromYAML.service, fromCode.service);
  });
});
