package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/config"
	"github.com/astraive/loza/cortex/internal/models"
	"github.com/astraive/loza/cortex/internal/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

type noopBatchProcessor struct{}

func (noopBatchProcessor) ProcessBatch(context.Context, []*models.Event) error { return nil }

// TestRunHTTPServerReportsListenError verifies that a listen failure is sent
// to the caller over a channel instead of terminating the process. If the
// server called log.Fatal on the error, this test binary would exit with 1.
func TestRunHTTPServerReportsListenError(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer occupied.Close()

	srv := &http.Server{Addr: occupied.Addr().String()}
	errCh := make(chan error, 1)
	go runHTTPServer(srv, errCh)

	select {
	case err := <-errCh:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("expected a listen error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("listen error was not reported to the caller")
	}
}

// TestRunHTTPServerIgnoresGracefulShutdown verifies that a normal Shutdown
// does not report a spurious error that would otherwise trigger the failure
// path in main.
func TestRunHTTPServerIgnoresGracefulShutdown(t *testing.T) {
	srv := &http.Server{Addr: "127.0.0.1:0"}
	errCh := make(chan error, 1)
	go runHTTPServer(srv, errCh)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("unexpected error after graceful shutdown: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestStartCollectorSyncReturnsWaitableCompletion(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	cfg.Collector.SourceOfTruth = true
	cfg.Collector.Mode = "pull"

	ctx, cancel := context.WithCancel(context.Background())
	done := startCollectorSync(ctx, cfg.Collector, noopBatchProcessor{})
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("collector sync did not stop after cancellation")
	}
}

func TestShutdownGRPCServerStopsLateHandoff(t *testing.T) {
	// Reproduces the reported defect: shutdown begins before the server is
	// handed off. The fix must still reach and stop the server rather than
	// observing a nil global and silently skipping graceful shutdown.
	ready := make(chan *grpc.Server, 1)
	srv := grpc.NewServer()

	go func() {
		time.Sleep(20 * time.Millisecond)
		ready <- srv
	}()

	shutdownGRPCServer(ready)

	// A stopped server rejects Serve with ErrServerStopped.
	lis := bufconn.Listen(1024)
	if err := srv.Serve(lis); !errors.Is(err, grpc.ErrServerStopped) {
		t.Fatalf("gRPC server was not stopped after late handoff: got %v", err)
	}
}

func TestShutdownGRPCServerNoServerReturnsPromptly(t *testing.T) {
	// A setup failure closes the channel without a server; shutdown must not
	// block waiting for one that will never arrive.
	ready := make(chan *grpc.Server)
	close(ready)

	done := make(chan struct{})
	go func() {
		shutdownGRPCServer(ready)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked waiting for a server that will not arrive")
	}
}

func TestShutdownGRPCServerNilChannel(t *testing.T) {
	// A nil channel means gRPC is disabled; shutdown is a no-op.
	done := make(chan struct{})
	go func() {
		shutdownGRPCServer(nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown with gRPC disabled should be a no-op")
	}
}

func TestStartGRPCServerClosesChannelOnSetupFailure(t *testing.T) {
	// TLS setup fails before a listener or server is created. The returned
	// channel must close without a value so callers never block on it.
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	cfg.TLS.Enabled = true
	cfg.TLS.CertFile = "/nonexistent/loza-cert.pem"
	cfg.TLS.KeyFile = "/nonexistent/loza-key.pem"

	ready := startGRPCServer(cfg, nil)
	select {
	case srv, ok := <-ready:
		if ok {
			srv.Stop()
			t.Fatal("unexpected gRPC server handed off despite TLS setup failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("startGRPCServer did not signal setup failure via channel close")
	}
}

type syncTestEventStore struct {
	saved [][]*models.Event
}

func (f *syncTestEventStore) Save(context.Context, *models.Event, *storage.LifecycleData) error {
	return nil
}

func (f *syncTestEventStore) SaveBatch(_ context.Context, events []*models.Event, _ []*storage.LifecycleData) error {
	f.saved = append(f.saved, events)
	return nil
}

func (*syncTestEventStore) Get(context.Context, string) (*models.Event, error) {
	return nil, errors.New("not found")
}
func (*syncTestEventStore) List(context.Context, int, int) ([]*models.Event, error) {
	return nil, nil
}
func (*syncTestEventStore) FindByTraceID(context.Context, string) ([]*models.Event, error) {
	return nil, nil
}
func (*syncTestEventStore) FindByIncidentID(context.Context, string) ([]*models.Event, error) {
	return nil, nil
}
func (*syncTestEventStore) FindByService(context.Context, string, string, string) ([]*models.Event, error) {
	return nil, nil
}
func (*syncTestEventStore) FindByEventName(context.Context, string, int, int) ([]*models.Event, error) {
	return nil, nil
}
func (*syncTestEventStore) FindByOutcome(context.Context, string, int, int) ([]*models.Event, error) {
	return nil, nil
}
func (*syncTestEventStore) FindByLevel(context.Context, string, int, int) ([]*models.Event, error) {
	return nil, nil
}
func (*syncTestEventStore) FindByDurationRange(context.Context, float64, float64, int, int) ([]*models.Event, error) {
	return nil, nil
}
func (*syncTestEventStore) FindByEnvironment(context.Context, string, int, int) ([]*models.Event, error) {
	return nil, nil
}
func (*syncTestEventStore) FindByRelease(context.Context, string, int, int) ([]*models.Event, error) {
	return nil, nil
}
func (*syncTestEventStore) AverageDuration(context.Context, string, time.Time, time.Time) (float64, error) {
	return 0, nil
}
func (*syncTestEventStore) CountByEventName(context.Context, string, time.Time, time.Time) (map[string]int64, error) {
	return nil, nil
}
func (*syncTestEventStore) CountByOutcome(context.Context, string, time.Time, time.Time) (map[string]int64, error) {
	return nil, nil
}
func (*syncTestEventStore) PercentileDuration(context.Context, string, float64, time.Time, time.Time) (float64, error) {
	return 0, nil
}
func (*syncTestEventStore) DistinctServices(context.Context) ([]string, error) { return nil, nil }
func (*syncTestEventStore) DistinctEventNames(context.Context) ([]string, error) {
	return nil, nil
}
func (*syncTestEventStore) ListLifecycleSummaries(context.Context, *storage.LifecycleFilter) ([]*models.LifecycleSummary, int, error) {
	return nil, 0, nil
}

type syncTestTopologyStore struct{}

func (*syncTestTopologyStore) SaveAlias(context.Context, *models.ServiceAlias) error { return nil }
func (*syncTestTopologyStore) GetAlias(context.Context, string, string) (*models.ServiceAlias, error) {
	return nil, errors.New("not found")
}
func (*syncTestTopologyStore) GetHistory(context.Context, string) ([]*models.ServiceAlias, error) {
	return nil, nil
}

type syncTestGraphStore struct{}

func (*syncTestGraphStore) SaveNode(context.Context, *models.Node) error { return nil }
func (*syncTestGraphStore) GetNode(context.Context, string) (*models.Node, error) {
	return nil, errors.New("not found")
}
func (*syncTestGraphStore) ListNodes(context.Context, string, int) ([]*models.Node, error) {
	return nil, nil
}
func (*syncTestGraphStore) SaveEdge(context.Context, *models.Edge) error { return nil }
func (*syncTestGraphStore) GetEdges(context.Context, string, string) ([]*models.Edge, error) {
	return nil, nil
}
func (*syncTestGraphStore) Traverse(context.Context, string, models.TraversalOptions) (*models.GraphView, error) {
	return nil, nil
}

type syncTestStorage struct {
	events   *syncTestEventStore
	topology *syncTestTopologyStore
	graph    *syncTestGraphStore
}

func (*syncTestStorage) Init(context.Context) error { return nil }
func (*syncTestStorage) Close() error               { return nil }
func (s *syncTestStorage) Events() storage.EventStore {
	return s.events
}
func (s *syncTestStorage) Topology() storage.TopologyStore {
	return s.topology
}
func (s *syncTestStorage) Graph() storage.GraphStore {
	return s.graph
}
func (*syncTestStorage) Incidents() storage.IncidentStore       { return nil }
func (*syncTestStorage) Signatures() storage.SignatureStore     { return nil }
func (*syncTestStorage) Remediations() storage.RemediationStore { return nil }
func (*syncTestStorage) Feedback() storage.FeedbackStore        { return nil }

func TestSyncProcessorAppliesPIIRedaction(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.PIIRedaction.Enabled = true
	cfg.PIIRedaction.Mode = "enforce"

	events := &syncTestEventStore{}
	stor := &syncTestStorage{
		events:   events,
		topology: &syncTestTopologyStore{},
		graph:    &syncTestGraphStore{},
	}

	// The source-of-truth sync feeds pulled events through ProcessBatch.
	proc := newSyncProcessor(cfg, stor)
	batch := []*models.Event{{
		ID:         "evt-sync",
		Timestamp:  time.Now(),
		Kind:       models.EventKindLog,
		Service:    "worker",
		Provenance: "collector",
		Raw: map[string]any{
			"email": "bob@example.com",
			"note":  "contact bob@example.com for details",
		},
	}}
	if err := proc.ProcessBatch(context.Background(), batch); err != nil {
		t.Fatalf("process batch: %v", err)
	}
	if len(events.saved) != 1 || len(events.saved[0]) != 1 {
		t.Fatalf("expected one saved batch event, got %+v", events.saved)
	}
	raw := events.saved[0][0].Raw
	if got := raw["email"]; got != "[REDACTED]" {
		t.Fatalf("sync email not redacted: %v", got)
	}
	if got := raw["note"]; got != "contact [REDACTED] for details" {
		t.Fatalf("sync pattern PII not redacted: %v", got)
	}
}
