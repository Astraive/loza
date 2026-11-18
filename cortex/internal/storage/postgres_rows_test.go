package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

// errIteration simulates a failure that happens after row iteration has
// already started, i.e. after rows.Next() has returned at least one row.
var errIteration = errors.New("simulated mid-stream row iteration failure")

var fakeRowsDriverSeq atomic.Int64

// newFakeRowsDB returns a *sql.DB backed by a driver that emits rowCount valid
// rows and then returns tailErr from Next(). When tailErr is nil it returns
// io.EOF, i.e. a normal end of iteration.
func newFakeRowsDB(t *testing.T, columns []string, values []driver.Value, rowCount int, tailErr error) *sql.DB {
	t.Helper()

	name := fmt.Sprintf("loza-fake-rows-%d", fakeRowsDriverSeq.Add(1))
	sql.Register(name, fakeRowsDriver{
		columns:  columns,
		values:   values,
		rowCount: rowCount,
		tailErr:  tailErr,
	})

	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type fakeRowsDriver struct {
	columns  []string
	values   []driver.Value
	rowCount int
	tailErr  error
}

func (d fakeRowsDriver) Open(string) (driver.Conn, error) { return fakeRowsConn{d}, nil }

type fakeRowsConn struct{ d fakeRowsDriver }

func (c fakeRowsConn) Prepare(string) (driver.Stmt, error) { return fakeRowsStmt{c.d}, nil }
func (c fakeRowsConn) Close() error                        { return nil }
func (c fakeRowsConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported by the fake driver")
}

type fakeRowsStmt struct{ d fakeRowsDriver }

func (s fakeRowsStmt) Close() error  { return nil }
func (s fakeRowsStmt) NumInput() int { return -1 }
func (s fakeRowsStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("exec is not supported by the fake driver")
}
func (s fakeRowsStmt) Query([]driver.Value) (driver.Rows, error) {
	return &fakeRows{d: s.d}, nil
}

type fakeRows struct {
	d       fakeRowsDriver
	emitted int
}

func (r *fakeRows) Columns() []string { return r.d.columns }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.emitted >= r.d.rowCount {
		if r.d.tailErr != nil {
			return r.d.tailErr
		}
		return io.EOF
	}
	copy(dest, r.d.values)
	r.emitted++
	return nil
}

// eventColumns/eventValues mirror pgSelectCols and scanPostgresRow.
func eventColumns() []string {
	return []string{
		"id", "event_id", "timestamp", "service", "environment", "release",
		"schema_version", "event_version", "event", "kind", "level", "outcome", "duration_ms",
		"trace_id", "span_id", "trace_flags", "request_id",
		"http", "user", "tenant", "attrs", "error",
		"checkpoints", "processes", "groups", "timers", "links",
		"sdk_name", "sdk_version", "sdk_language", "raw", "provenance", "incident_id", "created_at",
	}
}

func eventValues() []driver.Value {
	now := time.Now()
	return []driver.Value{
		"event-1", "evt-1", now, "checkout", "prod", "v1",
		"1", "1", "checkout.completed", "event", "info", "success", float64(12.5),
		"trace-1", "span-1", "01", "req-1",
		[]byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`),
		[]byte(`[]`), []byte(`[]`), []byte(`[]`), []byte(`[]`), []byte(`[]`),
		"sdk", "1.0", "go", []byte(`{}`), "test", "incident-1", now,
	}
}

func TestPostgresEventStoreListPropagatesRowIterationError(t *testing.T) {
	db := newFakeRowsDB(t, eventColumns(), eventValues(), 1, errIteration)
	store := &PostgresEventStore{db: db}

	events, err := store.List(context.Background(), 10, 0)
	if !errors.Is(err, errIteration) {
		t.Fatalf("List() error = %v, want %v", err, errIteration)
	}
	if len(events) != 0 {
		t.Fatalf("List() returned %d partial events when iteration failed, want 0", len(events))
	}
}

func TestPostgresEventStoreCountByOutcomePropagatesRowIterationError(t *testing.T) {
	db := newFakeRowsDB(t, []string{"outcome", "cnt"}, []driver.Value{"success", int64(3)}, 1, errIteration)
	store := &PostgresEventStore{db: db}

	result, err := store.CountByOutcome(context.Background(), "checkout",
		time.Now().Add(-time.Hour), time.Now())
	if !errors.Is(err, errIteration) {
		t.Fatalf("CountByOutcome() error = %v, want %v", err, errIteration)
	}
	if result != nil {
		t.Fatalf("CountByOutcome() returned partial result %v when iteration failed, want nil", result)
	}
}

func TestPostgresEventStoreListReturnsCompleteResultsOnCleanIteration(t *testing.T) {
	db := newFakeRowsDB(t, eventColumns(), eventValues(), 2, nil)
	store := &PostgresEventStore{db: db}

	events, err := store.List(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("List() returned %d events, want 2", len(events))
	}
}
