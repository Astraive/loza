package storage

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/astraive/loza/cortex/internal/models"
)

// The lifecycle JSON columns are positional arguments 23..26 (zero-based
// 22..25) of the INSERT statements in postgres.go / duckdb.go.
const (
	colCheckpoints = 22
	colProcesses   = 23
	colGroups      = 24
	colTimers      = 25
	colLinks       = 26
)

// captureDriver is a minimal database/sql driver that records the values the
// storage layer writes, so tests can assert what is actually persisted without
// requiring a live Postgres or DuckDB database.
type captureDriver struct {
	mu   sync.Mutex
	rows [][]driver.Value
}

func (d *captureDriver) Open(string) (driver.Conn, error) { return &captureConn{d: d}, nil }

func (d *captureDriver) record(vals []driver.Value) {
	cp := append([]driver.Value(nil), vals...)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rows = append(d.rows, cp)
}

func (d *captureDriver) snapshot() [][]driver.Value {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([][]driver.Value, len(d.rows))
	for i, row := range d.rows {
		out[i] = append([]driver.Value(nil), row...)
	}
	return out
}

type captureConn struct{ d *captureDriver }

func (c *captureConn) Prepare(string) (driver.Stmt, error) { return &captureStmt{d: c.d}, nil }
func (c *captureConn) Close() error                        { return nil }
func (c *captureConn) Begin() (driver.Tx, error)           { return &captureTx{}, nil }
func (c *captureConn) ExecContext(_ context.Context, _ string, args []driver.NamedValue) (driver.Result, error) {
	vals := make([]driver.Value, len(args))
	for i, a := range args {
		vals[i] = a.Value
	}
	c.d.record(vals)
	return driver.RowsAffected(1), nil
}

type captureStmt struct{ d *captureDriver }

func (s *captureStmt) Close() error  { return nil }
func (s *captureStmt) NumInput() int { return -1 }
func (s *captureStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.d.record(args)
	return driver.RowsAffected(1), nil
}
func (s *captureStmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, errors.New("captureStmt: queries not supported")
}

type captureTx struct{}

func (*captureTx) Commit() error   { return nil }
func (*captureTx) Rollback() error { return nil }

var captureDriverSeq atomic.Int64

func newCaptureDB(t *testing.T) (*sql.DB, *captureDriver) {
	t.Helper()
	d := &captureDriver{}
	name := fmt.Sprintf("loza-lifecycle-capture-%d", captureDriverSeq.Add(1))
	sql.Register(name, d)
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("open capture db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, d
}

func checkpointNames(t *testing.T, value driver.Value) []string {
	t.Helper()
	raw, ok := value.([]byte)
	if !ok {
		t.Fatalf("expected []byte column, got %T", value)
	}
	var cps []*models.EventCheckpoint
	if err := json.Unmarshal(raw, &cps); err != nil {
		t.Fatalf("unmarshal checkpoints %q: %v", raw, err)
	}
	names := make([]string, 0, len(cps))
	for _, cp := range cps {
		names = append(names, cp.Name)
	}
	return names
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func columnContains(t *testing.T, value driver.Value, want string) {
	t.Helper()
	raw, ok := value.([]byte)
	if !ok {
		t.Fatalf("expected []byte column, got %T", value)
	}
	if !bytes.Contains(raw, []byte(want)) {
		t.Fatalf("column %q does not contain %q", raw, want)
	}
}

func TestSavePersistsCallerLifecycleData(t *testing.T) {
	db, captured := newCaptureDB(t)
	store := &PostgresEventStore{db: db}

	// The event itself carries no lifecycle arrays; the caller-supplied
	// LifecycleData is the only place these primitives live. Before the fix
	// this data was silently discarded via the unused lifecycle parameter.
	event := &models.Event{ID: "evt-1", Service: "svc"}
	lifecycle := &LifecycleData{
		Checkpoints: []*models.EventCheckpoint{{Name: "lifecycle-cp"}},
		Processes:   []*models.EventProcess{{Name: "lifecycle-pr"}},
		Groups:      []*models.EventGroup{{Name: "lifecycle-gr"}},
		Timers:      []*models.EventTimer{{Name: "lifecycle-ti"}},
		Links:       []*models.EventLink{{Target: "lifecycle-li"}},
	}

	if err := store.Save(context.Background(), event, lifecycle); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rows := captured.snapshot()
	if len(rows) != 1 {
		t.Fatalf("expected 1 write, got %d", len(rows))
	}
	for col, want := range map[int]string{
		colCheckpoints: "lifecycle-cp",
		colProcesses:   "lifecycle-pr",
		colGroups:      "lifecycle-gr",
		colTimers:      "lifecycle-ti",
		colLinks:       "lifecycle-li",
	} {
		columnContains(t, rows[0][col], want)
	}
}

func TestSaveBatchPersistsPerEventLifecycleData(t *testing.T) {
	db, captured := newCaptureDB(t)
	store := &PostgresEventStore{db: db}

	events := []*models.Event{
		{ID: "evt-1", Service: "svc"},
		{ID: "evt-2", Service: "svc"},
	}
	lifecycles := []*LifecycleData{
		{Checkpoints: []*models.EventCheckpoint{{Name: "first"}}},
		{Checkpoints: []*models.EventCheckpoint{{Name: "second"}}},
	}

	if err := store.SaveBatch(context.Background(), events, lifecycles); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	rows := captured.snapshot()
	if len(rows) != 2 {
		t.Fatalf("expected 2 writes, got %d", len(rows))
	}
	if got := checkpointNames(t, rows[0][colCheckpoints]); !equalStrings(got, []string{"first"}) {
		t.Fatalf("first row checkpoints = %v, want [first]", got)
	}
	if got := checkpointNames(t, rows[1][colCheckpoints]); !equalStrings(got, []string{"second"}) {
		t.Fatalf("second row checkpoints = %v, want [second]", got)
	}
}

func TestResolveLifecycleArraysFallsBackToEvent(t *testing.T) {
	event := &models.Event{
		Checkpoints: []*models.EventCheckpoint{{Name: "event-cp"}},
		Timers:      []*models.EventTimer{{Name: "event-timer"}},
	}

	// No lifecycle data: arrays embedded in the event remain authoritative.
	cp, _, _, timers, _ := resolveLifecycleArrays(event, nil)
	if len(cp) != 1 || cp[0].Name != "event-cp" {
		t.Fatalf("nil lifecycle fallback checkpoints = %v", cp)
	}
	if len(timers) != 1 || timers[0].Name != "event-timer" {
		t.Fatalf("nil lifecycle fallback timers = %v", timers)
	}

	// Partial lifecycle data: only the arrays it supplies override the event.
	lifecycle := &LifecycleData{
		Checkpoints: []*models.EventCheckpoint{{Name: "lifecycle-cp"}},
	}
	cp, _, _, timers, _ = resolveLifecycleArrays(event, lifecycle)
	if len(cp) != 1 || cp[0].Name != "lifecycle-cp" {
		t.Fatalf("lifecycle checkpoints = %v, want [lifecycle-cp]", cp)
	}
	if len(timers) != 1 || timers[0].Name != "event-timer" {
		t.Fatalf("expected event timer fallback, got %v", timers)
	}
}
