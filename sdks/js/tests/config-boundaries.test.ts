import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  collectorRouteURL, defaultConfig, disabled, validateConfig, withOptions,
  development, dev, production, test as testPreset, ConfigBuilder,
  WithService, WithAlias, WithVersion, WithEnvironment, WithSink, WithSampler,
  WithRedactor, WithSchema, WithEventSchema, WithAsync, WithCollectorEndpoint,
  WithDuplicatePolicy, WithBasicAuth, WithStatsHandler, WithDeploymentID,
  WithIncludeHost, WithPanicRecovery, WithApiKey, WithRelease, WithNamespace,
  WithOtelBridge, WithRetry, WithTimeout, WithQueueSize, WithLogger,
} from '../src/config/config.ts';
import { memorySink } from '../src/sinks/standard-sinks.ts';
import { sampleNone } from '../src/sampling/sampler.ts';
import { redact } from '../src/redaction/redactor.ts';
import { OTelSchema } from '../src/core/schema.ts';
import { Logger } from '../src/core/logger.ts';
import { Event } from '../src/core/event.ts';

const noopLogger = new Logger({ service: '' });
describe('Config boundaries', () => {
  it('builds presets and applies every fluent option', () => {
    assert.equal(collectorRouteURL('http://collector///', 'tenant one', '/events'), 'http://collector/collectors/tenant%20one/events');
    assert.equal(collectorRouteURL('http://collector/', '', '/events'), 'http://collector/events');
    assert.equal(disabled().sink, null);
    assert.equal(development('dev').environment, 'development');
    assert.equal(dev('dev').environment, 'development');
    assert.equal(production('prod').strict, true);
    assert.equal(production('prod').async.enabled, true);
    assert.equal(testPreset().service, 'test');

    const sink = memorySink();
    const builder = new ConfigBuilder(defaultConfig());
    const configured = builder
      .withService('svc').withAlias('alias').withVersion('1').withEnvironment('test')
      .withCollectorUrl('http://localhost:9308/events').withCollectorName('collector')
      .withApiKey(' key ').withBasicAuth('', '').withSink(sink).withSinks(sink)
      .withSampler(sampleNone()).withRedactor(redact('secret')).withSchema(new OTelSchema())
      .withLevel('debug').withStrict(true).withDuplicatePolicy('merge').withAsync(true)
      .withCollectorEndpoint('http://localhost:9308/events').withStatsHandler({})
      .withDeploymentID('dep').withIncludeHost(false).withPanicRecovery(true)
      .withBatchSize(2).withFlushInterval(20).withEnableCompression(false).withRelease('r')
      .withNamespace('ns').withOtelBridge(true).withRetry(1).withTimeout(20).withQueueSize(3)
      .withLogger(noopLogger);
    assert.equal(configured, builder);
    const disabledConfig = builder.disabled();
    const event = new Event({ event: 'test' }, 'svc', 'test');
    assert.equal(disabledConfig.sampler(event), false);
  });

  it('applies all ConfigOption helpers and preserves defaults for compatibility no-ops', () => {
    const sink = memorySink();
    const schema = new OTelSchema();
    let cfg = defaultConfig();
    const options = [
      WithService('svc'), WithAlias('alias'), WithVersion('1'), WithEnvironment('prod'),
      WithSink(sink), WithSampler(sampleNone()), WithRedactor(redact('token')), WithSchema(schema),
      WithEventSchema(schema), WithAsync(true), WithCollectorEndpoint('http://localhost:9308'),
      WithDuplicatePolicy('drop'), WithBasicAuth('lz_pub_capability', ''), WithStatsHandler({}),
      WithDeploymentID('dep'), WithIncludeHost(false), WithPanicRecovery(true), WithApiKey(' key '),
      WithRelease('release'), WithNamespace('ns'), WithOtelBridge(true), WithRetry(2),
      WithTimeout(100), WithQueueSize(4), WithLogger(noopLogger),
    ];
    for (const option of options) cfg = option(cfg);
    assert.equal(cfg.service, 'svc');
    assert.equal(cfg.alias, 'alias');
    assert.equal(cfg.apiKey, 'key');
    assert.equal(cfg.async.enabled, true);
    assert.equal(cfg.async.queueSize, 4);
    assert.equal(cfg.maxRetries, 2);
    assert.equal(cfg.timeoutMs, 100);
    assert.equal(cfg.includeHost, false);
  });

  it('applies sink, logger and other declared options through the functional option API', () => {
    const sink = memorySink();
    const childLogger = new Logger({ service: 'svc' });

    // The headline defect: WithSink must actually install the sink on the config.
    assert.equal(WithSink(sink)(defaultConfig()).sink, sink);

    // withOptions must apply the declared fields, not silently drop them.
    const viaOptions = withOptions(defaultConfig(), { sink, logger: childLogger });
    assert.equal(viaOptions.sink, sink);
    assert.equal(viaOptions.logger, childLogger);

    // Logger.child() routes through withOptions, so the sink must survive there too.
    const viaChild = new Logger({ service: 'svc' }).child({ sink });
    assert.equal(viaChild.getConfig().sink, sink);

    // The remaining declared ConfigOptions must also be applied, not ignored.
    const stats = { onCollectorAck() {} };
    assert.equal(WithStatsHandler(stats)(defaultConfig()).statsHandler, stats);
    assert.equal(WithDeploymentID('dep')(defaultConfig()).deploymentId, 'dep');
    assert.equal(WithPanicRecovery(true)(defaultConfig()).panicRecovery, true);
    assert.equal(WithLogger(childLogger)(defaultConfig()).logger, childLogger);
  });

  it('rejects invalid credential and endpoint combinations', () => {
    const embedded = defaultConfig();
    embedded.collectorUrl = 'https://user:pass@example.com/events';
    assert.throws(() => validateConfig(embedded), /credentials must not be embedded/);
    const passwordOnly = defaultConfig();
    passwordOnly.collectorUrl = 'https://collector.example/events';
    passwordOnly.password = 'secret';
    assert.throws(() => validateConfig(passwordOnly), /password requires a username/);
    const usernameOnly = defaultConfig();
    usernameOnly.collectorUrl = 'https://collector.example/events';
    usernameOnly.username = 'private-user';
    assert.throws(() => validateConfig(usernameOnly), /require a password/);
    const plaintext = defaultConfig();
    plaintext.collectorUrl = 'http://collector.example/events';
    plaintext.username = 'user';
    plaintext.password = 'pass';
    assert.throws(() => validateConfig(plaintext), /require HTTPS/);
    // Regression: API keys must not be sent over cleartext HTTP to non-local hosts.
    const plaintextApiKey = defaultConfig();
    plaintextApiKey.collectorUrl = 'http://collector.example/events';
    plaintextApiKey.apiKey = 'secret-key';
    assert.throws(() => validateConfig(plaintextApiKey), /require HTTPS/);
    const httpsApiKey = defaultConfig();
    httpsApiKey.collectorUrl = 'https://collector.example/events';
    httpsApiKey.apiKey = 'secret-key';
    assert.equal(validateConfig(httpsApiKey), httpsApiKey);
    const localApiKey = defaultConfig();
    localApiKey.collectorUrl = 'http://localhost/events';
    localApiKey.apiKey = 'secret-key';
    assert.equal(validateConfig(localApiKey), localApiKey);
    const localhost = defaultConfig();
    localhost.collectorUrl = 'http://localhost/events';
    localhost.username = 'user';
    localhost.password = 'pass';
    assert.equal(validateConfig(localhost), localhost);
  });

  it('requires HTTPS for API-key credentials on non-local hosts', () => {
    const apiKeyOnly = defaultConfig();
    apiKeyOnly.collectorUrl = 'http://collector.example/events';
    apiKeyOnly.apiKey = 'secret-key';
    assert.throws(() => validateConfig(apiKeyOnly), /require HTTPS/);

    const apiKeyAndBasic = defaultConfig();
    apiKeyAndBasic.collectorUrl = 'http://collector.example/events';
    apiKeyAndBasic.apiKey = 'secret-key';
    apiKeyAndBasic.username = 'user';
    apiKeyAndBasic.password = 'pass';
    assert.throws(() => validateConfig(apiKeyAndBasic), /require HTTPS/);

    const localhost = defaultConfig();
    localhost.collectorUrl = 'http://localhost:9308/events';
    localhost.apiKey = 'secret-key';
    assert.equal(validateConfig(localhost), localhost);

    const https = defaultConfig();
    https.collectorUrl = 'https://collector.example/events';
    https.apiKey = 'secret-key';
    assert.equal(validateConfig(https), https);

    const noCredentials = defaultConfig();
    noCredentials.collectorUrl = 'http://collector.example/events';
    assert.equal(validateConfig(noCredentials), noCredentials);
  it('allows Basic credentials over plaintext HTTP to IPv6 localhost ([::1])', () => {
    // URL.hostname keeps IPv6 brackets, so the bare '::1' comparison missed this.
    const ipv6 = defaultConfig();
    ipv6.collectorUrl = 'http://[::1]:9308/events';
    ipv6.username = 'user';
    ipv6.password = 'pass';
    assert.equal(validateConfig(ipv6), ipv6);

    // The full reported path: a loza:// DSN with credentials on [::1] resolves
    // to http://[::1]:9308 with Basic auth and must also pass validation.
    const fromDSN = withOptions(defaultConfig(), {
      collectorUrl: 'loza://user:pass@[::1]/col',
    });
    assert.equal(fromDSN.collectorUrl, 'http://[::1]:9308');
    assert.equal(fromDSN.username, 'user');
    assert.equal(fromDSN.password, 'pass');

    // Non-localhost IPv6 endpoints must still be rejected over plaintext HTTP.
    const remote = defaultConfig();
    remote.collectorUrl = 'http://[::2]:9308/events';
    remote.username = 'user';
    remote.password = 'pass';
    assert.throws(() => validateConfig(remote), /require HTTPS/);
  });

  it('handles collector DSNs with public capabilities', () => {
    const cfg = withOptions(defaultConfig(), {
      collectorUrl: 'loza://lz_pub_capability:@localhost:9308/project?service=payments&env=staging',
    });
    assert.equal(cfg.collectorUrl, 'http://localhost:9308');
    assert.equal(cfg.collectorName, 'project');
    assert.equal(cfg.username, 'lz_pub_capability');
    assert.equal(cfg.password, '');
    assert.equal(cfg.service, 'payments');
    assert.equal(cfg.environment, 'staging');
  });
  it('applies every direct option and both OTEL bridge branches', () => {
    const base = defaultConfig();
    const cfg = withOptions(base, {
      service: 'svc', alias: 'alias', version: '1', environment: 'test', release: 'r', namespace: 'ns',
      collectorUrl: 'http://localhost:9308', collectorName: 'collector', apiKey: 'key',
      username: 'user', password: 'pass', sampler: sampleNone(), redactor: redact('secret'),
      schema: new OTelSchema(), level: 'debug', strict: true, async: true, asyncQueueSize: 4,
      workers: 2, batchSize: 3, flushIntervalMs: 4, enableCompression: false, duplicatePolicy: 'drop',
      includeHost: false,
    });
    assert.equal(cfg.service, 'svc');
    assert.equal(cfg.async.queueSize, 4);
    assert.equal(cfg.async.workers, 2);
    assert.equal(cfg.includeHost, false);
    assert.equal(WithOtelBridge(false)(base), base);
  });
});
