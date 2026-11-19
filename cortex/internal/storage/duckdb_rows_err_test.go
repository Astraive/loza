package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"
)

// errConnector builds a *sql.DB whose result set contains one valid row and
// then fails during iteration. This reproduces the database/sql contract that
// rows.Next() may stop because of an error rather than because of exhaustion.
type errConnector struct {
	row []driver.Value
	err error
}

func (c errConnector) Connect(context.Context) (driver.Conn, error) {
	return &errConn{c: c}, nil
}

func (errConnector) Driver() driver.Driver { return errDriver{} }

type errDriver struct{}

func (errDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("not implemented")
}

type errConn struct{ c errConnector }

func (*errConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not implemented")
}
func (*errConn) Close() error              { return nil }
func (*errConn) Begin() (driver.Tx, error) { return nil, errors.New("not implemented") }

func (c *errConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &errRows{row: c.c.row, err: c.c.err}, nil
}

type errRows struct {
	row  []driver.Value
	err  error
	sent bool
}

func (r *errRows) Columns() []string {
	return make([]string, len(r.row))
}

func (*errRows) Close() error { return nil }

func (r *errRows) Next(dest []driver.Value) error {
	if !r.sent {
		copy(dest, r.row)
		r.sent = true
		return nil
	}
	if r.err != nil {
		err := r.err
		r.err = nil
		return err
	}
	return io.EOF
}

func newIterationErrorDB(t *testing.T, row []driver.Value, iterErr error) *sql.DB {
	t.Helper()
	db := sql.OpenDB(errConnector{row: row, err: iterErr})
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func eventRow() []driver.Value {
	now := time.Now().UTC()
	nullJSON := []byte("null")
	return []driver.Value{
		"evt-1", "event-1", now, "checkout", "prod", "v1", // id .. release
		"1", "1", "http.request", "request", "info", "success", 12.5, // schema .. duration_ms
		"trace-1", "span-1", "00", "req-1", // trace fields
		nullJSON, nullJSON, nullJSON, nullJSON, nullJSON, // http, user, tenant, attrs, error
		nullJSON, nullJSON, nullJSON, nullJSON, nullJSON, // checkpoints, processes, groups, timers, links
		"sdk", "1.0", "go", // SDK metadata
		nullJSON, "manual", "incident-1", now, // raw, provenance, incident_id, created_at
	}
}

func TestQueryAllEventsReturnsIterationError(t *testing.T) {
	iterErr := errors.New("duckdb: error while scanning rows")
	db := newIterationErrorDB(t, eventRow(), iterErr)

	events, err := queryAllEvents(context.Background(), db, "")
	if !errors.Is(err, iterErr) {
		t.Fatalf("expected iteration error %v, got %v (events: %d)", iterErr, err, len(events))
	}
	if len(events) != 0 {
		t.Fatalf("expected no partial results on iteration error, got %d events", len(events))
	}
}

func TestQueryAllEventsReturnsRowsWhenIterationSucceeds(t *testing.T) {
	db := newIterationErrorDB(t, eventRow(), nil)

	events, err := queryAllEvents(context.Background(), db, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 || events[0].ID != "evt-1" {
		t.Fatalf("expected one scanned event, got %+v", events)
	}
}

func TestDistinctServicesReturnsIterationError(t *testing.T) {
	iterErr := errors.New("duckdb: error while scanning rows")
	db := newIterationErrorDB(t, []driver.Value{"payments"}, iterErr)

	store := &DuckDBEventStore{db: db}
	services, err := store.DistinctServices(context.Background())
	if !errors.Is(err, iterErr) {
		t.Fatalf("expected iteration error %v, got %v (services: %d)", iterErr, err, len(services))
	}
	if len(services) != 0 {
		t.Fatalf("expected no partial results on iteration error, got %d services", len(services))
	}
}
