package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/astraive/loza/collector/internal/auth"
	lozav1 "github.com/astraive/loza/gen/go/loza/core"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type mockState struct {
	ready   bool
	healthy bool
	metrics Metrics
}

func (m *mockState) IsReady() bool   { return m.ready }
func (m *mockState) IsHealthy() bool { return m.healthy }
func (m *mockState) Ingest(ctx context.Context, events [][]byte) (int, error) {
	m.metrics.EventsAccepted += int64(len(events))
	return len(events), nil
}
func (m *mockState) GetMetrics() Metrics { return m.metrics }

func TestHTTPServerDisabled(t *testing.T) {
	cfg := HTTPConfig{Enabled: false}
	state := &mockState{ready: true, healthy: true}

	srv := NewHTTPServer(cfg, state)
	assert.Equal(t, "http", srv.Name())
	assert.False(t, srv.IsReady())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := srv.Start(ctx)
	assert.NoError(t, err)
	assert.False(t, srv.IsReady())
}

func TestHTTPServerIngest(t *testing.T) {
	cfg := HTTPConfig{
		Enabled:      true,
		Addr:         "127.0.0.1:0",
		IngestPath:   "/ingest",
		HealthPath:   "/healthz",
		ReadyPath:    "/readyz",
		MaxBodyBytes: 10 * 1024 * 1024,
		AuthEnabled:  false,
	}
	state := &mockState{ready: true, healthy: true}

	srv := NewHTTPServer(cfg, state)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	require.True(t, srv.IsReady())

	_ = srv.Stop(context.Background())
}

// Regression test for MASS-0213: auth must fail closed when the configured
// secret or header name is empty. Previously an empty AuthValue made
// ConstantTimeCompare("" , "") return 1, so a request with no credential was
// granted unauthenticated ingest.
func TestHTTPServerIngestAuthFailsClosedWithEmptyConfiguredSecret(t *testing.T) {
	const (
		authHeader = "X-API-Key"
		secret     = "lz_sec_live_ksec1_testsecret"
	)
	body := `{"event_id":"evt-1","timestamp":"2026-08-24T00:00:00Z"}` + "\n"

	tests := []struct {
		name      string
		authValue string
		hdrName   string
		presented string
		wantCode  int
	}{
		{
			name:      "empty secret with missing header is rejected",
			authValue: "",
			hdrName:   authHeader,
			presented: "",
			wantCode:  http.StatusUnauthorized,
		},
		{
			name:      "empty secret with arbitrary header is rejected",
			authValue: "",
			hdrName:   authHeader,
			presented: "anything",
			wantCode:  http.StatusUnauthorized,
		},
		{
			name:      "empty header name is rejected",
			authValue: secret,
			hdrName:   "",
			presented: secret,
			wantCode:  http.StatusUnauthorized,
		},
		{
			name:      "configured secret with missing header is rejected",
			authValue: secret,
			hdrName:   authHeader,
			presented: "",
			wantCode:  http.StatusUnauthorized,
		},
		{
			name:      "configured secret with wrong header is rejected",
			authValue: secret,
			hdrName:   authHeader,
			presented: "wrong-secret",
			wantCode:  http.StatusUnauthorized,
		},
		{
			name:      "configured secret with matching header is accepted",
			authValue: secret,
			hdrName:   authHeader,
			presented: secret,
			wantCode:  http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := HTTPConfig{
				Enabled:      true,
				IngestPath:   "/ingest",
				MaxBodyBytes: 10 * 1024 * 1024,
				AuthEnabled:  true,
				AuthHeader:   tt.hdrName,
				AuthValue:    tt.authValue,
			}
			state := &mockState{ready: true, healthy: true}
			srv := NewHTTPServer(cfg, state)

			req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(body))
			if tt.presented != "" && tt.hdrName != "" {
				req.Header.Set(tt.hdrName, tt.presented)
			}
			rec := httptest.NewRecorder()

			srv.handleIngest(rec, req)

			require.Equal(t, tt.wantCode, rec.Code, "response body: %s", rec.Body.String())
			if tt.wantCode == http.StatusUnauthorized {
				assert.Zero(t, state.metrics.EventsAccepted, "unauthorized request must not ingest events")
				assert.Contains(t, rec.Body.String(), "Unauthorized")
			} else {
				assert.Equal(t, int64(1), state.metrics.EventsAccepted)
			}
		})
	}
}

func TestHTTPServerHealthReady(t *testing.T) {
	cfg := HTTPConfig{
		Enabled:    true,
		Addr:       "127.0.0.1:0",
		IngestPath: "/ingest",
		HealthPath: "/healthz",
		ReadyPath:  "/readyz",
	}
	state := &mockState{ready: true, healthy: true}

	srv := NewHTTPServer(cfg, state)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = srv.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	assert.True(t, srv.IsReady())

	_ = srv.Stop(context.Background())
}

func TestGRPCServerDisabled(t *testing.T) {
	cfg := GRPCConfig{Enabled: false}
	state := &mockState{ready: true, healthy: true}

	srv := NewGRPCServer(cfg, state)
	assert.Equal(t, "grpc", srv.Name())
	assert.False(t, srv.IsReady())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := srv.Start(ctx)
	assert.NoError(t, err)
	assert.False(t, srv.IsReady())
}

func TestGRPCServerStart(t *testing.T) {
	cfg := GRPCConfig{
		Enabled:              true,
		Port:                 "127.0.0.1:0",
		MaxConcurrentStreams: 100,
	}
	state := &mockState{ready: true, healthy: true}

	srv := NewGRPCServer(cfg, state)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	require.True(t, srv.IsReady())

	_ = srv.Stop(context.Background())
}

func TestGRPCCollectorServiceHealth(t *testing.T) {
	svc := &collectorSvcServer{state: &mockState{healthy: true}}

	resp, err := svc.Health(context.Background(), &CollectorStatusRequest{})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Status)

	svc.state = &mockState{healthy: false}
	resp, err = svc.Health(context.Background(), &CollectorStatusRequest{})
	require.NoError(t, err)
	assert.Equal(t, "unhealthy", resp.Status)
}

func TestGRPCCollectorServiceReady(t *testing.T) {
	svc := &collectorSvcServer{state: &mockState{ready: true}}

	resp, err := svc.Ready(context.Background(), &CollectorStatusRequest{})
	require.NoError(t, err)
	assert.Equal(t, "ready", resp.Status)

	svc.state = &mockState{ready: false}
	resp, err = svc.Ready(context.Background(), &CollectorStatusRequest{})
	require.NoError(t, err)
	assert.Equal(t, "not_ready", resp.Status)
}

func TestGRPCLogIngestServicePush(t *testing.T) {
	state := &mockState{ready: true, healthy: true}
	svc := &logIngestSvcServer{state: state}

	batch := &EventBatch{
		Events: []*Event{
			{RawJson: `{"id":"1"}`},
			{RawJson: `{"id":"2"}`},
		},
	}

	resp, err := svc.Push(context.Background(), batch)
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.Accepted)
	assert.Equal(t, int64(2), state.metrics.EventsAccepted)
}

func TestGRPCLogIngestServicePushEmpty(t *testing.T) {
	state := &mockState{ready: true, healthy: true}
	svc := &logIngestSvcServer{state: state}

	resp, err := svc.Push(context.Background(), &EventBatch{})
	require.NoError(t, err)
	assert.Equal(t, int64(0), resp.Accepted)
}

func TestGRPCLogIngestServicePushNil(t *testing.T) {
	state := &mockState{ready: true, healthy: true}
	svc := &logIngestSvcServer{state: state}

	resp, err := svc.Push(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0), resp.Accepted)
}

func TestLozaIngestServiceIngest(t *testing.T) {
	state := &mockState{ready: true, healthy: true}
	svc := &lozaIngestSvcServer{state: state}

	resp, err := svc.Ingest(context.Background(), &EventBatch{
		Events: []*Event{
			{RawJson: `{"id":"1"}`},
			{RawJson: `{"id":"2"}`},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.Accepted)
	assert.Equal(t, int64(2), state.metrics.EventsAccepted)
}

func TestLozaIngestServiceIngestStream(t *testing.T) {
	state := &mockState{ready: true, healthy: true}
	svc := &lozaIngestSvcServer{state: state}
	stream := &mockIngestStream{
		ctx: context.Background(),
		batches: []*EventBatch{
			{Events: []*Event{{RawJson: `{"id":"1"}`}}},
			{Events: []*Event{{RawJson: `{"id":"2"}`}, {RawJson: `{"id":"3"}`}}},
		},
	}

	err := svc.IngestStream(stream)
	require.NoError(t, err)
	require.NotNil(t, stream.closed)
	assert.Equal(t, int64(3), stream.closed.Accepted)
	assert.Equal(t, int64(3), state.metrics.EventsAccepted)
}

// partialIngestState accepts a fixed number of events so tests can exercise
// the Rejected counter returned by ingestGRPCBatch.
type partialIngestState struct {
	mockState
	accepted int
}

func (s *partialIngestState) Ingest(ctx context.Context, events [][]byte) (int, error) {
	return s.accepted, nil
}

func TestGRPCIngestBatchReportsMalformedEventsAsInvalid(t *testing.T) {
	state := &mockState{ready: true, healthy: true}
	svc := &logIngestSvcServer{state: state}

	batch := &EventBatch{
		Events: []*Event{
			nil,
			{RawJson: ""},
			{RawJson: `{"id":"1"}`},
		},
	}

	resp, err := svc.Push(context.Background(), batch)
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Accepted)
	assert.Equal(t, int64(2), resp.Invalid, "nil and empty-RawJson events must be reported as invalid")
	assert.Equal(t, int64(0), resp.Rejected)
	assert.Equal(t, int64(1), state.metrics.EventsAccepted, "only the valid event should reach Ingest")
}

func TestGRPCIngestBatchAllMalformedReportsInvalid(t *testing.T) {
	state := &mockState{ready: true, healthy: true}
	svc := &lozaIngestSvcServer{state: state}

	resp, err := svc.Ingest(context.Background(), &EventBatch{
		Events: []*Event{nil, {}, {RawJson: ""}},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), resp.Accepted)
	assert.Equal(t, int64(3), resp.Invalid, "an all-malformed batch must still report the dropped events")
	assert.Equal(t, int64(0), resp.Rejected)
	assert.Equal(t, int64(0), state.metrics.EventsAccepted)
}

func TestGRPCIngestBatchReportsRejected(t *testing.T) {
	state := &partialIngestState{accepted: 1}
	svc := &logIngestSvcServer{state: state}

	resp, err := svc.Push(context.Background(), &EventBatch{
		Events: []*Event{
			{RawJson: `{"id":"1"}`},
			{RawJson: `{"id":"2"}`},
			{RawJson: `{"id":"3"}`},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Accepted)
	assert.Equal(t, int64(2), resp.Rejected, "events passed to Ingest but not accepted must be reported as rejected")
	assert.Equal(t, int64(0), resp.Invalid)
}

func TestLozaIngestServiceIngestStreamReportsDroppedEvents(t *testing.T) {
	state := &mockState{ready: true, healthy: true}
	svc := &lozaIngestSvcServer{state: state}
	stream := &mockIngestStream{
		ctx: context.Background(),
		batches: []*EventBatch{
			{Events: []*Event{nil, {RawJson: `{"id":"1"}`}}},
			{Events: []*Event{{RawJson: ""}, {RawJson: `{"id":"2"}`}}},
		},
	}

	err := svc.IngestStream(stream)
	require.NoError(t, err)
	require.NotNil(t, stream.closed)
	assert.Equal(t, int64(2), stream.closed.Accepted)
	assert.Equal(t, int64(2), stream.closed.Invalid, "stream must aggregate the Invalid counter across batches")
	assert.Equal(t, int64(0), stream.closed.Rejected)
	assert.Equal(t, int64(2), state.metrics.EventsAccepted)
}

func TestGraphQLServerDisabled(t *testing.T) {
	cfg := GraphQLConfig{Enabled: false}
	state := &mockState{ready: true, healthy: true}

	srv := NewGraphQLServer(cfg, state)
	assert.Equal(t, "graphql", srv.Name())
	assert.False(t, srv.IsReady())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := srv.Start(ctx)
	assert.NoError(t, err)
	assert.False(t, srv.IsReady())
}

func TestGraphQLServerStart(t *testing.T) {
	cfg := GraphQLConfig{
		Enabled:    true,
		Port:       "127.0.0.1:0",
		Playground: false,
	}
	state := &mockState{ready: true, healthy: true}

	srv := NewGraphQLServer(cfg, state)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = srv.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	require.True(t, srv.IsReady())

	_ = srv.Stop(context.Background())
}

func TestDuration(t *testing.T) {
	d := NewDuration(5 * time.Second)
	assert.Equal(t, 5*time.Second, d.Duration())
}

func TestParseNDJSONEvents(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected [][]byte
		maxBytes int64
	}{
		{
			name:     "empty",
			input:    "",
			expected: nil,
		},
		{
			name:     "single event",
			input:    `{"id":"1"}`,
			expected: [][]byte{[]byte(`{"id":"1"}`)},
		},
		{
			name:     "multiple events",
			input:    `{"id":"1"}` + "\n" + `{"id":"2"}`,
			expected: [][]byte{[]byte(`{"id":"1"}`), []byte(`{"id":"2"}`)},
		},
		{
			name:     "empty lines skipped",
			input:    `{"id":"1"}` + "\n\n" + `{"id":"2"}`,
			expected: [][]byte{[]byte(`{"id":"1"}`), []byte(`{"id":"2"}`)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := parseNDJSONEvents(
				strings.NewReader(tt.input),
				tt.maxBytes,
			)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, events)
		})
	}
}

func TestItoa(t *testing.T) {
	assert.Equal(t, "0", itoa(0))
	assert.Equal(t, "1", itoa(1))
	assert.Equal(t, "100", itoa(100))
	assert.Equal(t, "-1", itoa(-1))
}

func TestConfigStructs(t *testing.T) {
	cfg := Config{
		HTTP: HTTPConfig{
			Enabled:           true,
			Addr:              ":9308",
			ReadHeaderTimeout: 5 * time.Second,
			MaxBodyBytes:      10 * 1024 * 1024,
		},
		GRPC: GRPCConfig{
			Enabled:              true,
			Port:                 ":9309",
			MaxConcurrentStreams: 100,
		},
		GraphQL: GraphQLConfig{
			Enabled:    true,
			Port:       ":9310",
			Playground: true,
		},
	}

	assert.True(t, cfg.HTTP.Enabled)
	assert.Equal(t, ":9308", cfg.HTTP.Addr)
	assert.True(t, cfg.GRPC.Enabled)
	assert.Equal(t, ":9309", cfg.GRPC.Port)
	assert.True(t, cfg.GraphQL.Enabled)
	assert.Equal(t, ":9310", cfg.GraphQL.Port)
}

func TestParseTailFiltersCollectorAndEnvironment(t *testing.T) {
	req := httptest.NewRequest("GET", "/tail?collector=orders&environment=production&service=checkout", nil)
	filters, err := ParseTailFilters(req)
	require.NoError(t, err)
	require.Equal(t, "orders", filters.Collector)
	require.Equal(t, "production", filters.Environment)
	require.Equal(t, "checkout", filters.Service)

	matching := []byte(`{"collector":"orders","environment":"production","service":"checkout"}`)
	otherEnvironment := []byte(`{"collector":"orders","environment":"staging","service":"checkout"}`)
	require.True(t, RawMatchesTailFilters(matching, filters))
	require.False(t, RawMatchesTailFilters(otherEnvironment, filters))
}

type websocketTailState struct {
	filters    chan TailFilters
	subscriber chan chan []byte
	removed    chan struct{}
	history    [][]byte
}

func (s *websocketTailState) TailHistory(_ context.Context, filters TailFilters) ([][]byte, error) {
	s.filters <- filters
	return s.history, nil
}

func (*websocketTailState) TailMatches(_ context.Context, raw []byte, filters TailFilters) bool {
	return RawMatchesTailFilters(raw, filters)
}

func (s *websocketTailState) AddTailSubscriber(ch chan []byte) {
	s.subscriber <- ch
}

func (s *websocketTailState) RemoveTailSubscriber(_ chan []byte) {
	close(s.removed)
}

func TestTailWebSocketBrowserContract(t *testing.T) {
	state := &websocketTailState{
		filters:    make(chan TailFilters, 1),
		subscriber: make(chan chan []byte, 1),
		removed:    make(chan struct{}),
		history:    [][]byte{[]byte(`{"event_id":"evt-1","timestamp":"2026-08-24T00:00:00Z","service":"checkout"}`)},
	}
	credential := "lz_sec_live_ksec1_testsecret"
	handler := NewTailWebSocketHandler(HTTPConfig{
		AuthEnabled: true,
		AuthHeader:  "X-API-Key",
		AuthValue:   credential,
	}, state)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	authProtocol := auth.WebSocketAuthProtocolPrefix +
		base64.RawURLEncoding.EncodeToString([]byte(credential))
	dialer := websocket.Dialer{Subprotocols: []string{auth.WebSocketTailProtocol, authProtocol}}
	conn, response, err := dialer.Dial(
		"ws"+strings.TrimPrefix(srv.URL, "http")+"?environment=production&service=checkout",
		nil,
	)
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		t.Fatalf("dial tail websocket: %v", err)
	}

	if got := conn.Subprotocol(); got != auth.WebSocketTailProtocol {
		t.Fatalf("negotiated protocol = %q, want %q", got, auth.WebSocketTailProtocol)
	}
	if _, raw, err := conn.ReadMessage(); err != nil || string(raw) == "" {
		t.Fatalf("read initial history: %q, %v", raw, err)
	}
	filters := <-state.filters
	if filters.Service != "checkout" {
		t.Fatalf("service filter = %q, want checkout", filters.Service)
	}
	if filters.Environment != "production" {
		t.Fatalf("environment filter = %q, want production", filters.Environment)
	}

	subscriber := <-state.subscriber
	_ = conn.Close()
	subscriber <- []byte(`{"service":"checkout"}`)
	select {
	case <-state.removed:
	case <-time.After(time.Second):
		t.Fatal("tail subscriber was not removed after disconnect")
	}
}

// TestTailWebSocketFiltersHistoryFrames verifies that initial history frames are
// passed through TailMatches before being written, just like live events. A
// history frame that does not match the requested service filter must never be
// delivered to the client (regression for MASS-0218 / issue #537).
func TestTailWebSocketFiltersHistoryFrames(t *testing.T) {
	nonMatchingHistory := []byte(`{"event_id":"evt-hist-skip","timestamp":"2026-08-24T00:00:00Z","service":"payments"}`)
	matchingHistory := []byte(`{"event_id":"evt-hist-ok","timestamp":"2026-08-24T00:00:01Z","service":"checkout"}`)
	state := &websocketTailState{
		filters:    make(chan TailFilters, 1),
		subscriber: make(chan chan []byte, 1),
		removed:    make(chan struct{}),
		history:    [][]byte{nonMatchingHistory, matchingHistory},
	}
	handler := NewTailWebSocketHandler(HTTPConfig{}, state)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	dialer := websocket.Dialer{Subprotocols: []string{auth.WebSocketTailProtocol}}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"?service=checkout", nil)
	if err != nil {
		t.Fatalf("dial tail websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	filters := <-state.filters
	if filters.Service != "checkout" {
		t.Fatalf("service filter = %q, want checkout", filters.Service)
	}
	subscriber := <-state.subscriber

	// The first delivered frame must be the matching history event. If history
	// frames bypass TailMatches, the non-matching "payments" event is sent first.
	_, first, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read first frame: %v", err)
	}
	if !bytes.Contains(first, []byte(`"evt-hist-ok"`)) {
		t.Fatalf("first frame = %s, want matching history event evt-hist-ok", first)
	}

	// A subsequent live event must be the next frame received. If the
	// non-matching history event leaked through it would arrive here instead.
	subscriber <- []byte(`{"event_id":"evt-live","service":"checkout"}`)
	_, second, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read second frame: %v", err)
	}
	if !bytes.Contains(second, []byte(`"evt-live"`)) {
		t.Fatalf("second frame = %s, want live event (non-matching history frame leaked)", second)
	}
}

type mockIngestStream struct {
	ctx     context.Context
	batches []*EventBatch
	closed  *PushResponse
	index   int
}

func (m *mockIngestStream) SetHeader(metadata.MD) error  { return nil }
func (m *mockIngestStream) SendHeader(metadata.MD) error { return nil }
func (m *mockIngestStream) SetTrailer(metadata.MD)       {}
func (m *mockIngestStream) Context() context.Context     { return m.ctx }
func (m *mockIngestStream) SendMsg(interface{}) error    { return nil }
func (m *mockIngestStream) RecvMsg(interface{}) error    { return nil }

func (m *mockIngestStream) SendAndClose(resp *PushResponse) error {
	m.closed = resp
	return nil
}

func (m *mockIngestStream) Recv() (*EventBatch, error) {
	if m.index >= len(m.batches) {
		return nil, io.EOF
	}
	batch := m.batches[m.index]
	m.index++
	return batch, nil
}

// blockingIngestState blocks inside Ingest so a client stream is guaranteed to
// be in flight on the server. The entered channel signals that the stream
// reached the handler; Ingest returns only once the stream context is cancelled
// by a forced stop, mirroring a hung long-lived client.
type blockingIngestState struct {
	*mockState
	entered chan struct{}
}

func (s *blockingIngestState) Ingest(ctx context.Context, events [][]byte) (int, error) {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	// Hold the RPC open until the stream is actually cancelled by a forced
	// stop, mirroring a real long-lived client stream.
	<-ctx.Done()
	return len(events), nil
}

func newBlockingState() *blockingIngestState {
	return &blockingIngestState{
		mockState: &mockState{ready: true, healthy: true},
		entered:   make(chan struct{}, 1),
	}
}

// startTestGRPCServer starts a GRPCServer on a random local port and opens a
// client stream whose RPC is held open on the server via a blocking Ingest.
func startTestGRPCServer(t *testing.T, state State) (*GRPCServer, context.CancelFunc, <-chan error) {
	t.Helper()

	srv := NewGRPCServer(GRPCConfig{
		Enabled:              true,
		Port:                 "127.0.0.1:0",
		MaxConcurrentStreams: 100,
		MaxRecvMsgSize:       1 << 20,
		MaxSendMsgSize:       1 << 20,
	}, state)
	// Keep the graceful-stop fallback short so shutdown tests run quickly.
	srv.shutdownGracePeriod = 200 * time.Millisecond

	startCtx, startCancel := context.WithCancel(context.Background())
	t.Cleanup(startCancel)
	startErr := make(chan error, 1)
	go func() { startErr <- srv.Start(startCtx) }()

	require.Eventually(t, func() bool {
		return srv.IsReady() && srv.lis != nil
	}, 2*time.Second, 10*time.Millisecond)

	conn, err := grpc.NewClient(srv.lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	stream, err := lozav1.NewLozaIngestClient(conn).IngestStream(context.Background())
	require.NoError(t, err)
	require.NoError(t, stream.Send(&lozav1.RawEventBatch{
		Events: []*lozav1.RawEvent{{RawJson: `{"id":"1"}`}},
	}))

	blocking, ok := state.(*blockingIngestState)
	require.True(t, ok)
	select {
	case <-blocking.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("client stream did not reach the server handler")
	}

	return srv, startCancel, startErr
}

func TestGRPCServerStopHonorsDeadlineWithOpenStream(t *testing.T) {
	state := newBlockingState()
	srv, _, startErr := startTestGRPCServer(t, state)

	// With an in-flight stream, a deadline-bounded Stop must force the server
	// down instead of blocking forever inside GracefulStop.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stopCancel()

	stopDone := make(chan error, 1)
	go func() { stopDone <- srv.Stop(stopCtx) }()

	select {
	case err := <-stopDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Stop blocked despite ctx deadline while a stream was open")
	}
	assert.False(t, srv.IsReady())

	select {
	case <-startErr:
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after forced stop")
	}
}

func TestGRPCServerStartCtxCancelStopsWithOpenStream(t *testing.T) {
	state := newBlockingState()
	_, cancelStart, startErr := startTestGRPCServer(t, state)

	// Cancelling the lifecycle context must stop the server even while a
	// stream is open, without leaking a goroutine stuck in GracefulStop.
	cancelStart()

	select {
	case <-startErr:
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after lifecycle context cancellation with an open stream")
	}
}
