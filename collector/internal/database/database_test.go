package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
)

// countRows is a minimal database/sql driver that serves count rows with no
// LIMIT clause of its own. It records how many rows the consumer actually
// pulled so the test can prove the collector stopped early.
type countRows struct {
	remaining int
	nextCalls *int
}

func (r *countRows) Columns() []string { return []string{"v"} }
func (r *countRows) Close() error      { return nil }
func (r *countRows) Next(dest []driver.Value) error {
	if r.remaining <= 0 {
		return io.EOF
	}
	r.remaining--
	*r.nextCalls++
	dest[0] = int64(*r.nextCalls)
	return nil
}

type countConn struct {
	count     int
	nextCalls *int
}

func (c countConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c countConn) Close() error                        { return nil }
func (c countConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (c countConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &countRows{remaining: c.count, nextCalls: c.nextCalls}, nil
}

type countConnector struct {
	count     int
	nextCalls *int
}

func (c countConnector) Connect(context.Context) (driver.Conn, error) {
	return countConn{count: c.count, nextCalls: c.nextCalls}, nil
}
func (c countConnector) Driver() driver.Driver { return countDriver{} }

type countDriver struct{}

func (countDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unused") }

func newCountingSQL(count int, nextCalls *int) *SQL {
	return &SQL{DB: sql.OpenDB(countConnector{count: count, nextCalls: nextCalls}), Info: Metadata{Backend: "fake"}}
}

func TestSQLQueryWithLimitStopsMaterializingRows(t *testing.T) {
	nextCalls := 0
	conn := newCountingSQL(5, &nextCalls)
	defer conn.Close(context.Background())

	result, err := conn.QueryWithLimit(context.Background(), 2, "SELECT v")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("materialized %d rows, want 2", len(result.Rows))
	}
	if nextCalls != 2 {
		t.Fatalf("driver served %d rows, want exactly 2 (early break)", nextCalls)
	}
}

func TestSQLQueryWithoutLimitStillReturnsAllRows(t *testing.T) {
	nextCalls := 0
	conn := newCountingSQL(5, &nextCalls)
	defer conn.Close(context.Background())

	result, err := conn.Query(context.Background(), "SELECT v")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 5 {
		t.Fatalf("materialized %d rows, want 5", len(result.Rows))
	}
}

func TestCollectRowsHonorsLimitWithoutError(t *testing.T) {
	columns := []string{"v"}
	values := []int64{1, 2, 3, 4, 5}
	index := 0
	closed := false
	result, err := collectRows(
		columns,
		func() bool { return index < len(values) },
		func(dest ...any) error {
			*(dest[0].(*any)) = values[index]
			index++
			return nil
		},
		func() error { return nil },
		func() { closed = true },
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 2 || result.Rows[0][0] != int64(1) || result.Rows[1][0] != int64(2) {
		t.Fatalf("unexpected bounded rows: %#v", result.Rows)
	}
	if !closed {
		t.Fatal("collectRows did not close the row source")
	}
}
