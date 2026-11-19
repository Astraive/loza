package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	serverruntime "github.com/astraive/loza/collector/internal/server"
	duckdbsink "github.com/astraive/loza/collector/internal/sinks/duckdb"
	publichttp "github.com/astraive/loza/collector/server/http"
	"github.com/gorilla/websocket"
)

func TestParseTailFilters(t *testing.T) {
	req := httptest.NewRequest("GET", "/tail?since=2026-01-01T00:00:00Z&after_event_id=evt-1&service=checkout&kind=log&trace_id=tr-1&incident_id=inc-1&limit=25", nil)
	filters, err := serverruntime.ParseTailFilters(req)
	if err != nil {
		t.Fatalf("parse filters: %v", err)
	}
	if !filters.Since.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected since filter: %+v", filters)
	}
	if filters.AfterEventID != "evt-1" || filters.Service != "checkout" || filters.Kind != "log" || filters.TraceID != "tr-1" || filters.IncidentID != "inc-1" || filters.Limit != 25 {
		t.Fatalf("unexpected filters: %+v", filters)
	}
}

func TestTailWebSocketUpgraderUsesConfiguredOrigins(t *testing.T) {
	const consoleOrigin = "https://console.example.test"

	tests := []struct {
		name        string
		cfg         serverruntime.HTTPConfig
		origin      string
		wantAllowed bool
	}{
		{
			name:        "production rejects empty origin",
			cfg:         serverruntime.HTTPConfig{AllowedOrigins: []string{consoleOrigin}},
			origin:      "",
			wantAllowed: false,
		},
		{
			name:        "production rejects localhost",
			cfg:         serverruntime.HTTPConfig{AllowedOrigins: []string{consoleOrigin}},
			origin:      "http://localhost:3000",
			wantAllowed: false,
		},
		{
			name:        "production allows configured origin",
			cfg:         serverruntime.HTTPConfig{AllowedOrigins: []string{consoleOrigin}},
			origin:      consoleOrigin,
			wantAllowed: true,
		},
		{
			name: "development allows localhost",
			cfg: serverruntime.HTTPConfig{
				AllowedOrigins:           []string{consoleOrigin},
				DevAllowLocalhostOrigins: true,
			},
			origin:      "http://localhost:5173",
			wantAllowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upgrader := tailWebSocketUpgrader(tt.cfg)
			req := httptest.NewRequest("GET", "http://collector.example/tail", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if got := upgrader.CheckOrigin(req); got != tt.wantAllowed {
				t.Fatalf("tail upgrader CheckOrigin(origin=%q) = %v, want %v", tt.origin, got, tt.wantAllowed)
			}
		})
	}
}

func TestRawMatchesTailFilters(t *testing.T) {
	raw := []byte(`{"id":"evt-1","service":"checkout","kind":"log","trace_id":"tr-1","incident_id":"inc-1"}`)
	if !rawMatchesTailFilters(raw, serverruntime.TailFilters{Service: "checkout", Kind: "log", TraceID: "tr-1", IncidentID: "inc-1"}) {
		t.Fatal("expected raw payload to match filters")
	}
	if rawMatchesTailFilters(raw, serverruntime.TailFilters{Service: "billing"}) {
		t.Fatal("expected service mismatch to fail")
	}
}

func TestTailMatchesAuthorizedCollectorScope(t *testing.T) {
	state := &collectorState{}
	ctx := publichttp.WithAuthorizedCollector(context.Background(), "orders", "production")
	matching := []byte(`{"collector":"orders","environment":"production","service":"checkout"}`)
	crossScope := []byte(`{"collector":"payments","environment":"production","service":"checkout"}`)

	if !state.TailMatches(ctx, matching, serverruntime.TailFilters{Service: "checkout"}) {
		t.Fatal("expected event in the authorized scope to match")
	}
	if state.TailMatches(ctx, crossScope, serverruntime.TailFilters{Service: "checkout"}) {
		t.Fatal("cross-collector event must not match an authorized stream")
	}
}

// Regression test for MASS-0131: the live event loop must enforce the same
// authorized collector/environment scope that the historical path applies.
func TestHandleTailWebSocketRestrictsLiveEventsToAuthorizedScope(t *testing.T) {
	state := &collectorState{}
	ctx, cancel := context.WithCancel(context.Background())

	// Simulate the route protector that attaches the authorized scope before
	// the handler runs. A cancelable context lets the handler exit cleanly at
	// the end of the test (the handler itself does not watch for client close).
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scoped := publichttp.WithAuthorizedCollector(ctx, "orders", "production")
		state.handleTailWebSocket(w, r.WithContext(scoped))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	defer cancel()

	conn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/tail?service=checkout", nil)
	if err != nil {
		t.Fatalf("dial tail websocket: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		state.tailMu.Lock()
		subscribed := len(state.tailSubscribers) > 0
		state.tailMu.Unlock()
		if subscribed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tail subscriber was not registered")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Both events match the raw service filter, but only one belongs to the
	// authorized collector scope.
	state.broadcastTail([]byte(`{"collector":"payments","environment":"production","service":"checkout"}`))
	state.broadcastTail([]byte(`{"collector":"orders","environment":"production","service":"checkout"}`))

	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	seen := map[string]bool{}
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var resp struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(msg, &resp); err != nil {
			t.Fatalf("unmarshal live event %q: %v", msg, err)
		}
		if resp.Type == "event" {
			if collector, ok := resp.Data["collector"].(string); ok {
				seen[collector] = true
			}
		}
	}

	if !seen["orders"] {
		t.Fatal("in-scope live event was not delivered; live loop was not exercised")
	}
	if seen["payments"] {
		t.Fatal("live tail leaked an event from a collector outside the authorized scope")
	}
}

func TestQueryTailHistoryDecryptsBeforeFiltering(t *testing.T) {
	db, err := sql.Open("duckdb", filepath.Join(t.TempDir(), "tail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE events (raw VARCHAR, timestamp TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}

	key := "tail-history-encryption-key"
	local := []byte(`{"event_id":"evt-local","timestamp":"2026-08-24T04:00:00Z","collector":"local","service":"checkout"}`)
	other := []byte(`{"event_id":"evt-other","timestamp":"2026-08-24T04:00:01Z","collector":"other","service":"checkout"}`)
	sink, err := duckdbsink.New(duckdbsink.Config{
		DB:         db,
		Table:      "events",
		Schema:     map[string]string{"timestamp": "timestamp"},
		StoreRaw:   true,
		RawColumn:  "raw",
		EncryptRaw: true,
		EncryptKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close(context.Background())
	for _, raw := range [][]byte{local, other} {
		if err := sink.WriteEvent(context.Background(), raw, nil); err != nil {
			t.Fatal(err)
		}
	}

	state := &collectorState{
		cfg: collectorConfig{
			duckDBTable:          "events",
			duckDBRawColumn:      "raw",
			duckDBSchema:         map[string]string{"timestamp": "timestamp"},
			storageEncryptionKey: key,
		},
		queryDB: db,
	}
	rows, err := state.queryTailHistory(context.Background(), serverruntime.TailFilters{
		Collector: "local",
		Limit:     10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || string(rows[0]) != string(local) {
		t.Fatalf("encrypted tail history = %q, want %q", rows, local)
	}
}

func TestTailTimestampColumnSingleCandidate(t *testing.T) {
	state := &collectorState{
		cfg: collectorConfig{
			duckDBSchema: map[string]string{
				"event_ts": "timestamp",
				"id":       "id",
			},
		},
	}
	col, err := state.tailTimestampColumn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if col != "event_ts" {
		t.Fatalf("timestamp column = %q, want %q", col, "event_ts")
	}
}

func TestTailTimestampColumnDefault(t *testing.T) {
	for _, schema := range []map[string]string{
		nil,
		{"id": "id"},
	} {
		state := &collectorState{cfg: collectorConfig{duckDBSchema: schema}}
		col, err := state.tailTimestampColumn()
		if err != nil {
			t.Fatalf("unexpected error for schema %v: %v", schema, err)
		}
		if col != "timestamp" {
			t.Fatalf("timestamp column = %q, want default %q", col, "timestamp")
		}
	}
}

// Regression test for MASS-0132: with more than one column mapped to the
// "timestamp" path, the old implementation returned whichever column Go's
// randomized map iteration happened to visit first. The resolved column must
// instead be deterministic; ambiguity is reported as a stable error.
func TestTailTimestampColumnAmbiguousIsDeterministic(t *testing.T) {
	const wantErr = "ambiguous timestamp column: 3 columns map to the timestamp path (alpha, mid, zeta)"

	// Build the map with different insertion orders across iterations to
	// exercise randomized map traversal.
	orders := [][]string{
		{"alpha", "mid", "zeta"},
		{"zeta", "mid", "alpha"},
		{"mid", "zeta", "alpha"},
	}
	for _, order := range orders {
		schema := map[string]string{"id": "id"}
		for _, col := range order {
			schema[col] = "timestamp"
		}
		state := &collectorState{cfg: collectorConfig{duckDBSchema: schema}}
		for i := 0; i < 50; i++ {
			col, err := state.tailTimestampColumn()
			if err == nil {
				t.Fatalf("expected ambiguity error, got column %q", col)
			}
			if err.Error() != wantErr {
				t.Fatalf("error = %q, want %q", err.Error(), wantErr)
			}
		}
	}
}

func TestQueryTailHistoryReportsAmbiguousTimestampColumn(t *testing.T) {
	state := &collectorState{
		cfg: collectorConfig{
			duckDBTable:     "events",
			duckDBRawColumn: "raw",
			duckDBSchema: map[string]string{
				"ts_a": "timestamp",
				"ts_b": "timestamp",
			},
		},
		queryDB: new(sql.DB),
	}
	_, err := state.queryTailHistory(context.Background(), serverruntime.TailFilters{Limit: 10})
	if err == nil {
		t.Fatal("expected queryTailHistory to reject an ambiguous timestamp column")
	}
	if !strings.Contains(err.Error(), "ambiguous timestamp column") {
		t.Fatalf("error = %q, want it to mention ambiguous timestamp column", err.Error())
func TestQueryTailHistorySkipsUndecryptableRows(t *testing.T) {
	db, err := sql.Open("duckdb", filepath.Join(t.TempDir(), "tail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE events (raw VARCHAR, timestamp TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}

	key := "tail-history-encryption-key"
	sinkCfg := func(encryptKey string) duckdbsink.Config {
		return duckdbsink.Config{
			DB:         db,
			Table:      "events",
			Schema:     map[string]string{"timestamp": "timestamp"},
			StoreRaw:   true,
			RawColumn:  "raw",
			EncryptRaw: true,
			EncryptKey: encryptKey,
		}
	}
	sink, err := duckdbsink.New(sinkCfg(key))
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close(context.Background())
	staleSink, err := duckdbsink.New(sinkCfg("stale-encryption-key"))
	if err != nil {
		t.Fatal(err)
	}
	defer staleSink.Close(context.Background())

	first := []byte(`{"event_id":"evt-first","timestamp":"2026-08-24T04:00:01Z","collector":"local","service":"checkout"}`)
	second := []byte(`{"event_id":"evt-second","timestamp":"2026-08-24T04:00:03Z","collector":"local","service":"checkout"}`)
	for _, raw := range [][]byte{first, second} {
		if err := sink.WriteEvent(context.Background(), raw, nil); err != nil {
			t.Fatal(err)
		}
	}

	// Valid ciphertext under a stale key fails authentication with the current key.
	stale := []byte(`{"event_id":"evt-stale","timestamp":"2026-08-24T04:00:04Z","collector":"local","service":"checkout"}`)
	if err := staleSink.WriteEvent(context.Background(), stale, nil); err != nil {
		t.Fatal(err)
	}

	// The encryption prefix with an invalid payload is a corrupt row.
	corrupt := "enc:not-valid-base64!!!"
	if _, err := db.Exec(`INSERT INTO events (raw, timestamp) VALUES (?, ?)`, corrupt, time.Date(2026, 8, 24, 4, 0, 2, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	state := &collectorState{
		cfg: collectorConfig{
			duckDBTable:          "events",
			duckDBRawColumn:      "raw",
			duckDBSchema:         map[string]string{"timestamp": "timestamp"},
			storageEncryptionKey: key,
		},
		queryDB: db,
	}
	rows, err := state.queryTailHistory(context.Background(), serverruntime.TailFilters{Limit: 10})
	if err != nil {
		t.Fatalf("query encrypted tail history: %v", err)
	}
	if len(rows) != 2 || string(rows[0]) != string(first) || string(rows[1]) != string(second) {
		t.Fatalf("encrypted tail history = %q, want [%q %q]", rows, first, second)
	}
func waitForTailSubscriberCount(t *testing.T, s *collectorState, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.tailMu.Lock()
		got := len(s.tailSubscribers)
		s.tailMu.Unlock()
		if got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.tailMu.Lock()
	got := len(s.tailSubscribers)
	s.tailMu.Unlock()
	t.Fatalf("tail subscriber count = %d, want %d", got, want)
}

// TestHandleTailWebSocketRemovesSubscriberOnClientDisconnect reproduces the
// finding that this handler never reads from the hijacked connection. Without a
// reader goroutine, a client disconnect is never observed (r.Context() is not
// cancelled for hijacked connections) and the subscriber registered by the
// handler leaks indefinitely.
func TestHandleTailWebSocketRemovesSubscriberOnClientDisconnect(t *testing.T) {
	state := &collectorState{}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		_ = http.Serve(ln, http.HandlerFunc(state.handleTailWebSocket))
	}()
	defer ln.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+ln.Addr().String()+"/tail", nil)
	if err != nil {
		t.Fatalf("dial tail websocket: %v", err)
	}

	// The handler registers a subscriber after upgrading the connection.
	waitForTailSubscriberCount(t, state, 1)

	// A disconnected client must not leak the subscriber. The handler can only
	// notice the disconnect by reading from the connection (close frame / read
	// error), since r.Context() is unreliable once the connection is hijacked.
	_ = conn.Close()
	waitForTailSubscriberCount(t, state, 0)
}
