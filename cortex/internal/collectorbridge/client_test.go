package collectorbridge

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/config"
	"github.com/astraive/loza/cortex/internal/models"
	"github.com/gorilla/websocket"
)

func TestFetchEventsSinceUsesScopedLQLRouteAndTypedCursorBindings(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotBody struct {
		Query      string                    `json:"query"`
		Parameters map[string]map[string]any `json:"parameters"`
		Limit      int                       `json:"limit"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if r.URL.Path == "/query" {
			http.Error(w, "legacy raw SQL route is forbidden", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"columns":["raw","ts"],"rows":[{"raw":"{\"event_id\":\"evt-1\",\"timestamp\":\"2026-01-01T00:00:01Z\",\"service\":\"checkout\",\"event\":\"checkout.request\"}","ts":"2026-01-01T00:00:01Z"}],"row_count":1}`))
	}))
	defer server.Close()

	client := NewClient(config.CollectorConfig{
		URL:       server.URL,
		Collector: "demo",
		APIKey:    "secret",
		BatchSize: 10,
	})
	cursor := Cursor{Timestamp: time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC), EventID: "evt-0"}
	events, next, err := client.FetchEventsSince(context.Background(), cursor, 10)
	if err != nil {
		t.Fatalf("fetch events: %v", err)
	}
	if gotPath != "/collectors/demo/lql/query" {
		t.Fatalf("unexpected route %q", gotPath)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("unexpected authorization header %q", gotAuth)
	}
	if !strings.Contains(gotBody.Query, "from events") || !strings.Contains(gotBody.Query, "$cursor_ts") || !strings.Contains(gotBody.Query, "$cursor_id") {
		t.Fatalf("query is not source-oriented or cursor-bound: %q", gotBody.Query)
	}
	if gotBody.Parameters["cursor_id"]["value"] != "evt-0" {
		t.Fatalf("cursor id binding missing: %#v", gotBody.Parameters)
	}
	if len(events) != 1 || events[0].ID != "evt-1" || next.EventID != "evt-1" {
		t.Fatalf("unexpected event result: events=%+v next=%+v", events, next)
	}
}

func TestCountByOutcomeUsesLQLSummarize(t *testing.T) {
	var gotPath string
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotQuery = body.Query
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"columns":["outcome","count"],"rows":[{"outcome":"success","count":3}],"row_count":1}`))
	}))
	defer server.Close()

	client := NewClient(config.CollectorConfig{URL: server.URL, Collector: "demo"})
	counts, err := client.CountByOutcome(context.Background(), "checkout", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("count outcomes: %v", err)
	}
	if gotPath != "/collectors/demo/lql/query" {
		t.Fatalf("unexpected route %q", gotPath)
	}
	if !strings.Contains(gotQuery, "summarize") || !strings.Contains(gotQuery, "count()") {
		t.Fatalf("query does not use LQL aggregation: %q", gotQuery)
	}
	if counts["success"] != 3 {
		t.Fatalf("unexpected counts: %#v", counts)
	}
}
func TestAverageDurationUsesScopedLQLAggregation(t *testing.T) {
	var gotPath string
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotQuery = body.Query
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"columns":["avg_dur"],"rows":[{"avg_dur":42.5}],"row_count":1}`))
	}))
	defer server.Close()

	client := NewClient(config.CollectorConfig{URL: server.URL, Collector: "demo"})
	average, err := client.AverageDuration(context.Background(), "checkout.request", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("average duration: %v", err)
	}
	if gotPath != "/collectors/demo/lql/query" {
		t.Fatalf("unexpected route %q", gotPath)
	}
	if !strings.Contains(gotQuery, "summarize") || !strings.Contains(gotQuery, "avg(duration_ms)") {
		t.Fatalf("query does not use LQL average aggregation: %q", gotQuery)
	}
	if average != 42.5 {
		t.Fatalf("average = %v, want 42.5", average)
	}
}

func TestPercentileDurationUsesNamedLQLPercentile(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotQuery = body.Query
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"columns":["p_dur"],"rows":[{"p_dur":99.0}],"row_count":1}`))
	}))
	defer server.Close()

	client := NewClient(config.CollectorConfig{URL: server.URL, Collector: "demo"})
	value, err := client.PercentileDuration(context.Background(), "checkout.request", 95, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("percentile duration: %v", err)
	}
	if !strings.Contains(gotQuery, "percentile(duration_ms, 95)") {
		t.Fatalf("query does not use source percentile syntax: %q", gotQuery)
	}
	if value != 99 {
		t.Fatalf("percentile = %v, want 99", value)
	}
}

func TestLifecycleSummariesUseScopedLQLProjectionAndCount(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		queries = append(queries, body.Query)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(body.Query, "count()") {
			_, _ = w.Write([]byte(`{"columns":["total"],"rows":[{"total":2}],"row_count":1}`))
			return
		}
		_, _ = w.Write([]byte(`{"columns":["event_id","event","service","outcome","duration_ms"],"rows":[{"event_id":"evt-1","event":"checkout.request","service":"checkout","outcome":"success","duration_ms":12.5}],"row_count":1}`))
	}))
	defer server.Close()

	client := NewClient(config.CollectorConfig{URL: server.URL, Collector: "demo"})
	rows, total, err := client.ListLifecycleSummaries(context.Background(), map[string]any{"service": "checkout"}, 100, 0)
	if err != nil {
		t.Fatalf("lifecycle summaries: %v", err)
	}
	if len(queries) != 2 || !strings.Contains(queries[0], "count()") || !strings.Contains(queries[1], "project") {
		t.Fatalf("unexpected lifecycle queries: %#v", queries)
	}
	if total != 2 || len(rows) != 1 || rows[0]["event_id"] != "evt-1" {
		t.Fatalf("rows=%#v total=%d", rows, total)
	}
}
func TestLQLQueriesUseBasicAuthAndScopeHeaders(t *testing.T) {
	var gotPath, gotAuth, gotEnv, gotService string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotEnv = r.Header.Get("X-Loza-Env")
		gotService = r.Header.Get("X-Loza-Service")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"columns":["raw"],"rows":[],"row_count":0}`))
	}))
	defer server.Close()

	client := NewClient(config.CollectorConfig{
		URL:         server.URL,
		Collector:   "demo",
		Username:    "query-user",
		Password:    "secret",
		Environment: "prod",
		Service:     "checkout",
	})
	if _, err := client.FindByService(context.Background(), "checkout", "", "", 10); err != nil {
		t.Fatalf("find by service: %v", err)
	}
	if gotPath != "/collectors/demo/lql/query" {
		t.Fatalf("unexpected route %q", gotPath)
	}
	if gotAuth != "Basic "+base64.StdEncoding.EncodeToString([]byte("query-user:secret")) {
		t.Fatalf("unexpected authorization header %q", gotAuth)
	}
	if gotEnv != "prod" || gotService != "checkout" {
		t.Fatalf("unexpected scope headers env=%q service=%q", gotEnv, gotService)
	}
}

func TestTailURLUsesConfiguredCollectorScope(t *testing.T) {
	client := &Client{cfg: config.CollectorConfig{
		URL:       "https://collector.example/base",
		Collector: "demo",
	}}

	if got, want := client.tailURL("/tail", false), "https://collector.example/base/collectors/demo/tail"; got != want {
		t.Fatalf("HTTP tail URL = %q, want %q", got, want)
	}
	if got, want := client.tailURL("/ws/tail", true), "wss://collector.example/base/collectors/demo/ws/tail"; got != want {
		t.Fatalf("WebSocket tail URL = %q, want %q", got, want)
	}
}

func TestTailURLIncludesSinceFromRecoveredCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collector.cursor")
	client := NewClient(config.CollectorConfig{
		URL:        "https://collector.example/base",
		Collector:  "demo",
		CursorPath: path,
	})
	backup := Cursor{Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EventID: "evt-1"}
	if err := client.SaveCursor(backup); err != nil {
		t.Fatalf("save backup cursor: %v", err)
	}
	if err := client.SaveCursor(Cursor{Timestamp: time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC), EventID: "evt-2"}); err != nil {
		t.Fatalf("save primary cursor: %v", err)
	}
	// Corrupt the primary so LoadCursor recovers the valid backup with ErrCursorRecovered.
	if err := os.WriteFile(path, []byte(`{"timestamp":`), 0o600); err != nil {
		t.Fatalf("corrupt primary cursor: %v", err)
	}

	u, err := url.Parse(client.tailURL("/tail", false))
	if err != nil {
		t.Fatalf("parse tail URL: %v", err)
	}
	q := u.Query()
	if got, want := q.Get("since"), backup.Timestamp.UTC().Format(time.RFC3339Nano); got != want {
		t.Fatalf("since = %q, want %q", got, want)
	}
	if got, want := q.Get("after_event_id"), backup.EventID; got != want {
		t.Fatalf("after_event_id = %q, want %q", got, want)
	}
}

func TestStreamTailHTTPSendsConfiguredScope(t *testing.T) {
	var gotPath, gotEnvironment string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotEnvironment = r.Header.Get("X-Loza-Env")
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"id":"tail-1","schema_version":"v1","event_version":"v1","event_id":"tail-1","timestamp":"2026-01-01T00:00:00Z","service":"checkout","event":"checkout.request","kind":"event"}` + "\n"))
	}))
	defer server.Close()

	client := NewClient(config.CollectorConfig{
		URL:         server.URL,
		Collector:   "demo",
		Environment: "prod",
	})
	var received int
	if err := client.StreamTail(context.Background(), func(_ *models.Event) error {
		received++
		return nil
	}); err != nil {
		t.Fatalf("stream tail: %v", err)
	}
	if gotPath != "/collectors/demo/tail" {
		t.Fatalf("tail path = %q, want scoped collector path", gotPath)
	}
	if gotEnvironment != "prod" {
		t.Fatalf("environment header = %q, want prod", gotEnvironment)
	}
	if received != 1 {
		t.Fatalf("received %d events, want 1", received)
	}
}

func TestCollectorAuthUsesConfiguredAPIKeyHeader(t *testing.T) {
	header := http.Header{}
	setCollectorAuth(header, config.CollectorConfig{
		APIKey:       "secret",
		APIKeyHeader: "X-Collector-Key",
	})

	if got := header.Get("X-Collector-Key"); got != "secret" {
		t.Fatalf("custom API-key header = %q, want secret", got)
	}
	if got := header.Get("Authorization"); got != "" {
		t.Fatalf("unexpected bearer authorization %q", got)
	}
}

func TestCursorPersistenceIsAtomicAndRecoversBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collector.cursor")
	client := NewClient(config.CollectorConfig{CursorPath: path})
	first := Cursor{Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EventID: "evt-1"}
	second := Cursor{Timestamp: time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC), EventID: "evt-2"}

	if err := client.SaveCursor(first); err != nil {
		t.Fatalf("save first cursor: %v", err)
	}
	if err := client.SaveCursor(second); err != nil {
		t.Fatalf("save second cursor: %v", err)
	}
	backupData, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("read cursor backup: %v", err)
	}
	backup, err := decodeCursor(backupData)
	if err != nil || backup != first {
		t.Fatalf("backup = %+v, err=%v; want %+v", backup, err, first)
	}

	if err := os.WriteFile(path, []byte(`{"timestamp":`), 0o600); err != nil {
		t.Fatalf("corrupt primary cursor: %v", err)
	}
	recovered, err := client.LoadCursor()
	if !errors.Is(err, ErrCursorRecovered) {
		t.Fatalf("load error = %v, want ErrCursorRecovered", err)
	}
	if recovered != first {
		t.Fatalf("recovered cursor = %+v, want %+v", recovered, first)
	}
}

func TestCursorPersistenceRejectsCorruptPrimaryAndBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collector.cursor")
	if err := os.WriteFile(path, []byte(`not-json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".bak", []byte(`also-not-json`), 0o600); err != nil {
		t.Fatal(err)
	}
	client := NewClient(config.CollectorConfig{CursorPath: path})
	if _, err := client.LoadCursor(); err == nil || errors.Is(err, ErrCursorRecovered) {
		t.Fatalf("load error = %v, want unrecoverable corruption", err)
	}
	if err := client.SaveCursor(Cursor{EventID: "evt-new"}); err == nil {
		t.Fatal("save must not overwrite an invalid primary cursor")
	}
}

// TestStreamTailWebSocketDoesNotLeakCancellationGoroutine verifies that the
// goroutine which closes the websocket on context cancellation also exits when
// streamTailWebSocket returns early on its own (for example, on a read error),
// even when the caller's context is never cancelled.
func TestStreamTailWebSocketDoesNotLeakCancellationGoroutine(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Close immediately so the client's ReadMessage fails and
		// streamTailWebSocket returns early while ctx is still live.
		_ = conn.Close()
	}))
	defer server.Close()

	client := NewClient(config.CollectorConfig{
		URL:           server.URL,
		Collector:     "demo",
		TailTransport: "websocket",
	})

	// Let the server and client settle so the baseline only reflects permanent
	// goroutines. The context below is context.Background(), which is never
	// cancelled -- this is the condition that previously leaked the goroutine.
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	if err := client.StreamTail(context.Background(), func(_ *models.Event) error {
		return nil
	}); err == nil {
		t.Fatal("expected websocket tail to return a read error after server close")
	}

	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > baseline {
		t.Fatalf("cancellation goroutine leaked: goroutines = %d, baseline = %d", got, baseline)
	}
}

// TestStreamTailWebSocketClosesConnectionOnContextCancellation keeps the
// original cancellation behavior covered: cancelling the context must unblock
// a live ReadMessage and make StreamTail return promptly.
func TestStreamTailWebSocketClosesConnectionOnContextCancellation(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Hold the connection open until the client cancels.
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	client := NewClient(config.CollectorConfig{
		URL:           server.URL,
		Collector:     "demo",
		TailTransport: "websocket",
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	if err := client.StreamTail(ctx, func(_ *models.Event) error {
		return nil
	}); err == nil {
		t.Fatal("expected websocket tail to return an error after context cancellation")
	}
}

// newTestCAAndLeaf issues a leaf certificate for 127.0.0.1/localhost signed by a
// freshly generated private CA. The returned PEM contains the CA certificate
// only, so it can be written to CollectorConfig.TLSCAFile.
func newTestCAAndLeaf(t *testing.T) (caPEM []byte, leaf tls.Certificate) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "loza-collectorbridge-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}
	leaf = tls.Certificate{
		Certificate: [][]byte{leafDER, caDER},
		PrivateKey:  leafKey,
	}
	return caPEM, leaf
}

func TestWebSocketDialerReusesCollectorTLSConfig(t *testing.T) {
	caPEM, _ := newTestCAAndLeaf(t)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	client := NewClient(config.CollectorConfig{URL: "https://collector.example", TLSCAFile: caPath})
	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs == nil {
		t.Fatal("expected HTTP transport to carry the configured root CAs")
	}
	got := client.webSocketDialer().TLSClientConfig
	if got != transport.TLSClientConfig || got.RootCAs != transport.TLSClientConfig.RootCAs {
		t.Fatal("websocket dialer must reuse the HTTP transport TLS config with the configured roots")
	}
	if websocket.DefaultDialer.TLSClientConfig != nil {
		t.Fatal("rebuilding the dialer must not mutate websocket.DefaultDialer")
	}
}

func TestStreamTailWebSocketUsesConfiguredTLSCA(t *testing.T) {
	caPEM, leaf := newTestCAAndLeaf(t)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("websocket upgrade: %v", err)
			return
		}
		defer conn.Close()
		if err := conn.WriteJSON(map[string]any{
			"type": "event",
			"data": map[string]any{
				"id":             "tls-tail-1",
				"schema_version": "v1",
				"event_version":  "v1",
				"event_id":       "tls-tail-1",
				"timestamp":      "2026-01-01T00:00:00Z",
				"service":        "checkout",
				"event":          "checkout.request",
				"kind":           "event",
			},
		}); err != nil {
			t.Errorf("write websocket event: %v", err)
		}
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{leaf}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()

	client := NewClient(config.CollectorConfig{
		URL:           server.URL,
		Collector:     "demo",
		TailTransport: "websocket",
		TLSCAFile:     caPath,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var received []*models.Event
	err := client.StreamTail(ctx, func(event *models.Event) error {
		received = append(received, event)
		cancel()
		return nil
	})
	if len(received) != 1 {
		t.Fatalf("received %d events, want 1 (err=%v)", len(received), err)
	}
	if received[0].ID != "tls-tail-1" {
		t.Fatalf("event ID = %q, want tls-tail-1", received[0].ID)
	}
	if err != nil && (strings.Contains(err.Error(), "x509") || strings.Contains(err.Error(), "certificate")) {
		t.Fatalf("unexpected TLS verification error: %v", err)
	}
}
