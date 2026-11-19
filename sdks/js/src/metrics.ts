export interface MetricsSnapshot {
  counters: Record<string, number>;
  gauges: Record<string, number>;
}

// Prometheus metric names must match [a-zA-Z_:][a-zA-Z0-9_:]*. The namespace
// and metric name are interpolated into the exposition format below, so any
// other character (notably a newline or space) is replaced with '_' and a
// leading digit is prefixed with '_'. This keeps emitted names valid and
// prevents caller input from injecting arbitrary exposition lines.
function sanitizeMetricName(input: string): string {
  const sanitized = input.replace(/[^a-zA-Z0-9_:]/g, '_');
  return /^[0-9]/.test(sanitized) ? `_${sanitized}` : sanitized;
}

export class MetricsCollector {
  private counters: Record<string, number> = {};
  private gauges: Record<string, number> = {};

  inc(name: string, amount = 1): void {
    this.counters[name] = (this.counters[name] ?? 0) + amount;
  }

  setGauge(name: string, value: number): void {
    this.gauges[name] = value;
  }

  snapshot(): MetricsSnapshot {
    return {
      counters: { ...this.counters },
      gauges: { ...this.gauges },
    };
  }

  renderPrometheus(namespace = 'loza_sdk'): string {
    const prefix = sanitizeMetricName(namespace.trim()) || 'loza_sdk';
    const lines: string[] = [];
    for (const [name, value] of Object.entries(this.counters)) {
      const metric = `${prefix}_${sanitizeMetricName(name)}`;
      lines.push(`# TYPE ${metric} counter`);
      lines.push(`${metric} ${value}`);
    }
    for (const [name, value] of Object.entries(this.gauges)) {
      const metric = `${prefix}_${sanitizeMetricName(name)}`;
      lines.push(`# TYPE ${metric} gauge`);
      lines.push(`${metric} ${value}`);
    }
    return `${lines.join('\n')}\n`;
  }
}

export function RenderPrometheus(metrics: MetricsCollector): string {
  return metrics.renderPrometheus();
}
