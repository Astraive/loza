package otlp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
)

func TestNewRejectsUnsupportedProtocol(t *testing.T) {
	_, err := New(context.Background(), Config{
		Protocol: "udp",
	})
	if err == nil {
		t.Fatalf("expected unsupported protocol error")
	}
	if !strings.Contains(err.Error(), "unsupported protocol") {
		t.Fatalf("expected unsupported protocol error, got: %v", err)
	}
}

func TestResolveConfigRejectsEndpointQuery(t *testing.T) {
	_, err := resolveConfig(Config{
		Endpoint: "https://otel.example:4317/core/logs?bad=1",
	})
	if err == nil {
		t.Fatalf("expected endpoint validation error")
	}
	if !strings.Contains(err.Error(), "must not include query") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveConfigRejectsInvalidRetryWindow(t *testing.T) {
	_, err := resolveConfig(Config{
		Retry: RetryConfig{
			InitialInterval: 2 * time.Second,
			MaxInterval:     time.Second,
		},
	})
	if err == nil {
		t.Fatalf("expected retry validation error")
	}
	if !strings.Contains(err.Error(), "max interval") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveConfigAppliesDefaults(t *testing.T) {
	cfg, err := resolveConfig(Config{})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.Protocol != "grpc" {
		t.Fatalf("expected default protocol grpc, got %q", cfg.Protocol)
	}
	if cfg.Logger != "loza-collector" {
		t.Fatalf("expected default logger loza-collector, got %q", cfg.Logger)
	}
	if cfg.Timeout != 10*time.Second {
		t.Fatalf("expected default timeout 10s, got %s", cfg.Timeout)
	}
	if !cfg.Retry.Enabled || cfg.Retry.InitialInterval <= 0 || cfg.Retry.MaxInterval < cfg.Retry.InitialInterval {
		t.Fatalf("unexpected default retry config: %+v", cfg.Retry)
	}
}

func TestNewResourceHonorsCallerResourceAttributes(t *testing.T) {
	res, err := newResource(context.Background(), resolvedConfig{
		Logger: "logger-service",
		ResourceAttributes: map[string]string{
			"service.name":       "caller-service",
			"telemetry.sdk.name": "caller-sdk",
			"custom.attribute":   "custom-value",
		},
	})
	if err != nil {
		t.Fatalf("new resource: %v", err)
	}

	got := resourceAttributes(res)
	assertResourceAttribute(t, got, "service.name", "caller-service")
	assertResourceAttribute(t, got, "telemetry.sdk.name", "caller-sdk")
	assertResourceAttribute(t, got, "custom.attribute", "custom-value")
	// Keys the caller did not override still come from the defaults.
	assertResourceAttribute(t, got, "telemetry.sdk.language", "go")
}

func TestNewResourceAppliesDefaultsWithoutCallerAttributes(t *testing.T) {
	res, err := newResource(context.Background(), resolvedConfig{Logger: "logger-service"})
	if err != nil {
		t.Fatalf("new resource: %v", err)
	}

	got := resourceAttributes(res)
	assertResourceAttribute(t, got, "service.name", "logger-service")
	assertResourceAttribute(t, got, "telemetry.sdk.name", "opentelemetry")
	assertResourceAttribute(t, got, "telemetry.sdk.language", "go")
	if got["telemetry.sdk.version"] == "" {
		t.Fatalf("expected SDK version to be set by WithTelemetrySDK")
	}
}

func resourceAttributes(res *resource.Resource) map[string]string {
	attrs := make(map[string]string)
	for _, kv := range res.Attributes() {
		attrs[string(kv.Key)] = kv.Value.AsString()
	}
	return attrs
}

func assertResourceAttribute(t *testing.T, attrs map[string]string, key, want string) {
	t.Helper()
	if got := attrs[key]; got != want {
		t.Fatalf("resource attribute %q: got %q, want %q", key, got, want)
type shutdownSpyExporter struct {
	shutdownCalled bool
	shutdownCtx    context.Context
}

func (*shutdownSpyExporter) Export(context.Context, []log.Record) error { return nil }
func (*shutdownSpyExporter) ForceFlush(context.Context) error           { return nil }
func (s *shutdownSpyExporter) Shutdown(ctx context.Context) error {
	s.shutdownCalled = true
	s.shutdownCtx = ctx
	return nil
}

// TestNewShutsDownExporterWhenResourceCreationFails verifies that a failed
// resource creation does not leak the already-started exporter (gRPC
// connection and background goroutines).
func TestNewShutsDownExporterWhenResourceCreationFails(t *testing.T) {
	origExporter, origResource := newExporterFn, newResourceFn
	t.Cleanup(func() {
		newExporterFn, newResourceFn = origExporter, origResource
	})

	spy := &shutdownSpyExporter{}
	newExporterFn = func(context.Context, resolvedConfig) (log.Exporter, error) {
		return spy, nil
	}
	newResourceFn = func(context.Context, ...resource.Option) (*resource.Resource, error) {
		return nil, errors.New("forced resource failure")
	}

	ctx := context.Background()
	_, err := New(ctx, Config{})
	if err == nil {
		t.Fatalf("expected resource creation error")
	}
	if !strings.Contains(err.Error(), "create resource") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !spy.shutdownCalled {
		t.Fatalf("expected exporter.Shutdown to be called when resource creation fails")
	}
	if spy.shutdownCtx != ctx {
		t.Fatalf("expected Shutdown to receive the provided context")
	}
}

func TestWriteEventRejectsEmptyPayload(t *testing.T) {
	provider := log.NewLoggerProvider()
	s := &sink{
		provider: provider,
		logger:   provider.Logger("test"),
	}
	err := s.WriteEvent(context.Background(), []byte("   \n"), nil)
	if err == nil {
		t.Fatalf("expected empty payload error")
	}
	if !strings.Contains(err.Error(), "empty encoded event") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type fakeExporter struct {
	err  error
	sent int
}

func (e *fakeExporter) Export(_ context.Context, records []log.Record) error {
	e.sent += len(records)
	return e.err
}

func (*fakeExporter) Shutdown(context.Context) error   { return nil }
func (*fakeExporter) ForceFlush(context.Context) error { return nil }

func newTestSink(processor log.Processor, tracker *deliveryErrorTracker) (*sink, func()) {
	provider := log.NewLoggerProvider(log.WithProcessor(processor))
	s := &sink{
		provider: provider,
		logger:   provider.Logger("test"),
		tracker:  tracker,
	}
	return s, func() { _ = provider.Shutdown(context.Background()) }
}

func TestWriteEventSurfacesExportFailure(t *testing.T) {
	exportErr := errors.New("endpoint refused the batch")
	exporter := &fakeExporter{err: exportErr}
	tracker := newDeliveryErrorTracker()
	s, cleanup := newTestSink(&errorCapturingProcessor{
		Processor: log.NewSimpleProcessor(exporter),
		tracker:   tracker,
	}, tracker)
	defer cleanup()

	err := s.WriteEvent(context.Background(), []byte(`{"event":"checkout.request"}`), nil)
	if err == nil {
		t.Fatal("expected WriteEvent to surface the export failure, got nil")
	}
	if !errors.Is(err, exportErr) {
		t.Fatalf("expected wrapped export error, got %v", err)
	}
	if exporter.sent != 1 {
		t.Fatalf("expected one exported record, got %d", exporter.sent)
	}
}

func TestWriteEventSucceedsAfterExportRecovers(t *testing.T) {
	exporter := &fakeExporter{err: errors.New("temporary failure")}
	tracker := newDeliveryErrorTracker()
	tracked := &trackingExporter{Exporter: exporter, tracker: tracker}

	if err := tracked.Export(context.Background(), []log.Record{{}}); err == nil {
		t.Fatal("expected the underlying export failure")
	}
	if err := tracker.load(); err == nil {
		t.Fatal("expected export failure to be latched")
	}

	exporter.err = nil
	if err := tracked.Export(context.Background(), []log.Record{{}}); err != nil {
		t.Fatalf("unexpected export error: %v", err)
	}

	s, cleanup := newTestSink(&errorCapturingProcessor{
		Processor: log.NewSimpleProcessor(exporter),
		tracker:   tracker,
	}, tracker)
	defer cleanup()

	if err := s.WriteEvent(context.Background(), []byte(`{"event":"recovered"}`), nil); err != nil {
		t.Fatalf("expected nil after a successful export cleared the failure, got %v", err)
	}
}

func TestWriteEventRejectsCanceledContext(t *testing.T) {
	s, cleanup := newTestSink(log.NewBatchProcessor(nil), newDeliveryErrorTracker())
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := s.WriteEvent(ctx, []byte(`{"event":"cancel"}`), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestWriteEventRejectsWritesAfterClose(t *testing.T) {
	s, _ := newTestSink(log.NewBatchProcessor(nil), newDeliveryErrorTracker())
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	err := s.WriteEvent(context.Background(), []byte(`{"event":"after-close"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("expected closed sink error, got %v", err)
	}
}
