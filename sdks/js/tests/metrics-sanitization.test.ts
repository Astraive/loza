import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { MetricsCollector, RenderPrometheus } from '../src/metrics.ts';

const METRIC_NAME = /^[a-zA-Z_:][a-zA-Z0-9_:]*$/;

function assertValidExposition(rendered: string): void {
  for (const line of rendered.split('\n')) {
    if (line === '' || line.startsWith('#')) continue;
    const name = line.split(' ')[0]!;
    assert.match(name, METRIC_NAME, `invalid metric name: ${name}`);
  }
}

describe('Prometheus exposition sanitization', () => {
  it('leaves valid namespaces and metric names unchanged', () => {
    const metrics = new MetricsCollector();
    metrics.inc('events_total');
    metrics.setGauge('queue:depth', 4);

    const rendered = metrics.renderPrometheus('svc_name:ok');
    assert.equal(
      rendered,
      '# TYPE svc_name:ok_events_total counter\nsvc_name:ok_events_total 1\n' +
        '# TYPE svc_name:ok_queue:depth gauge\nsvc_name:ok_queue:depth 4\n',
    );
    assertValidExposition(rendered);
  });

  it('prevents a namespace from injecting exposition lines', () => {
    const metrics = new MetricsCollector();
    metrics.inc('events');

    const clean = metrics.renderPrometheus('loza');
    const rendered = metrics.renderPrometheus('x\nfake_total 1');

    assert.equal(rendered.split('\n').length, clean.split('\n').length);
    assert.ok(!rendered.includes('fake_total 1'));
    assert.equal(rendered, '# TYPE x_fake_total_1_events counter\nx_fake_total_1_events 1\n');
    assertValidExposition(rendered);
    assert.equal(RenderPrometheus(metrics), metrics.renderPrometheus());
  });

  it('prevents a metric name from injecting exposition lines', () => {
    const metrics = new MetricsCollector();
    metrics.inc('evil\nfake_total 999');
    metrics.setGauge('bad name"with:chars', 2.5);

    const rendered = metrics.renderPrometheus();
    assert.equal(
      rendered,
      '# TYPE loza_sdk_evil_fake_total_999 counter\nloza_sdk_evil_fake_total_999 1\n' +
        '# TYPE loza_sdk_bad_name_with:chars gauge\nloza_sdk_bad_name_with:chars 2.5\n',
    );
    assertValidExposition(rendered);

    // Sanitization is render-only: the snapshot keeps the caller's keys.
    assert.deepEqual(metrics.snapshot(), {
      counters: { 'evil\nfake_total 999': 1 },
      gauges: { 'bad name"with:chars': 2.5 },
    });
  });

  it('handles leading digits and blank namespaces', () => {
    const metrics = new MetricsCollector();
    metrics.inc('9lives');

    const digitNamespace = metrics.renderPrometheus('9svc\nname');
    assert.equal(digitNamespace, '# TYPE _9svc_name__9lives counter\n_9svc_name__9lives 1\n');
    assertValidExposition(digitNamespace);

    const blankNamespace = metrics.renderPrometheus('  \t\r\n  ');
    assert.equal(blankNamespace, '# TYPE loza_sdk__9lives counter\nloza_sdk__9lives 1\n');
    assertValidExposition(blankNamespace);
  });
});
