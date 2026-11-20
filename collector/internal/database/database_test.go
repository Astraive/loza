package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"

	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/require"
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
		func() ([]any, error) {
			value := values[index]
			index++
			return []any{value}, nil
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

type stubColumnType struct {
	name     string
	scanType reflect.Type
	nullable bool
}

func (c stubColumnType) Name() string             { return c.name }
func (c stubColumnType) Nullable() bool           { return c.nullable }
func (c stubColumnType) ScanType() reflect.Type   { return c.scanType }
func (c stubColumnType) DatabaseTypeName() string { return c.name }

// stubClickHouseRows mirrors the parts of clickhouse-go that matter here:
// Nullable(T) columns report *T as their ScanType and the driver therefore
// writes through a **T destination (nil for SQL NULL).
type stubClickHouseRows struct {
	columns []string
	types   []chdriver.ColumnType
	data    [][]any
	pos     int
	err     error
	closed  bool
}

func (r *stubClickHouseRows) Columns() []string                  { return r.columns }
func (r *stubClickHouseRows) ColumnTypes() []chdriver.ColumnType { return r.types }
func (r *stubClickHouseRows) Err() error                         { return r.err }
func (r *stubClickHouseRows) Close() error {
	r.closed = true
	return nil
}

func (r *stubClickHouseRows) Next() bool {
	if r.pos+1 >= len(r.data) {
		return false
	}
	r.pos++
	return true
}

func (r *stubClickHouseRows) Scan(dest ...any) error {
	row := r.data[r.pos]
	if len(row) != len(dest) {
		return fmt.Errorf("Scan: expected %d destinations, got %d", len(row), len(dest))
	}
	for i, value := range row {
		switch d := dest[i].(type) {
		case *string:
			s, ok := value.(string)
			if !ok {
				return fmt.Errorf("Scan column %d: expected string, got %T", i, value)
			}
			*d = s
		case **string:
			if value == nil {
				*d = nil
				continue
			}
			s, ok := value.(string)
			if !ok {
				return fmt.Errorf("Scan column %d: expected string, got %T", i, value)
			}
			*d = new(string)
			**d = s
		case **int64:
			if value == nil {
				*d = nil
				continue
			}
			n, ok := value.(int64)
			if !ok {
				return fmt.Errorf("Scan column %d: expected int64, got %T", i, value)
			}
			*d = new(int64)
			**d = n
		case *[]byte:
			b, ok := value.([]byte)
			if !ok {
				return fmt.Errorf("Scan column %d: expected []byte, got %T", i, value)
			}
			*d = b
		default:
			return fmt.Errorf("Scan column %d: unexpected destination %T", i, dest[i])
		}
	}
	return nil
}

// fakeColumnType is a minimal driver.ColumnType for exercising the
// reflection-based ClickHouse scan path.
type fakeColumnType struct {
	name     string
	scanType reflect.Type
}

func (f fakeColumnType) Name() string             { return f.name }
func (f fakeColumnType) Nullable() bool           { return false }
func (f fakeColumnType) ScanType() reflect.Type   { return f.scanType }
func (f fakeColumnType) DatabaseTypeName() string { return "String" }

type fakeClickHouseRows struct {
	columns []string
	types   []chdriver.ColumnType
	data    [][]any
	pos     int
	rowErr  error
	scanErr error
	closed  bool
}

func (f *fakeClickHouseRows) Columns() []string                  { return f.columns }
func (f *fakeClickHouseRows) ColumnTypes() []chdriver.ColumnType { return f.types }

func (f *fakeClickHouseRows) Next() bool {
	if f.pos < len(f.data) {
		f.pos++
		return true
	}
	return false
}

func (f *fakeClickHouseRows) Scan(dest ...any) error {
	if f.scanErr != nil {
		return f.scanErr
	}
	row := f.data[f.pos-1]
	for i := range dest {
		pointer := reflect.ValueOf(dest[i])
		if pointer.Kind() != reflect.Ptr || pointer.IsNil() {
			continue
		}
		value := reflect.ValueOf(row[i])
		if value.IsValid() && value.Type().AssignableTo(pointer.Elem().Type()) {
			pointer.Elem().Set(value)
		}
	}
	return nil
}

func assertResultRows(t *testing.T, got, want [][]any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("row count = %d, want %d: %#v", len(got), len(want), got)
	}
	for r := range want {
		if len(got[r]) != len(want[r]) {
			t.Fatalf("row %d has %d values, want %d: %#v", r, len(got[r]), len(want[r]), got[r])
		}
		for c := range want[r] {
			g, w := got[r][c], want[r][c]
			if reflect.TypeOf(g) != reflect.TypeOf(w) || !reflect.DeepEqual(g, w) {
				t.Errorf("row %d column %d: got %T(%#v), want %T(%#v)", r, c, g, g, w, w)
			}
		}
	}
}

// Result.Rows must have the same concrete value shape for ClickHouse as for
// the SQL/Postgres paths: no driver pointer wrappers and no raw []byte text.
func TestQueryClickHouseRowsNormalizesValues(t *testing.T) {
	rows := &stubClickHouseRows{
		columns: []string{"plain", "nullable_text", "nullable_num", "null_text", "raw"},
		types: []chdriver.ColumnType{
			stubColumnType{name: "plain", scanType: reflect.TypeOf("")},
			stubColumnType{name: "nullable_text", scanType: reflect.TypeOf((*string)(nil)), nullable: true},
			stubColumnType{name: "nullable_num", scanType: reflect.TypeOf((*int64)(nil)), nullable: true},
			stubColumnType{name: "null_text", scanType: reflect.TypeOf((*string)(nil)), nullable: true},
			stubColumnType{name: "raw", scanType: reflect.TypeOf([]byte{})},
		},
		data: [][]any{
			{"plain-value", "text-value", int64(42), nil, []byte("bytes-value")},
			{"second", nil, int64(7), nil, []byte("more-bytes")},
		},
		pos: -1,
	}

	result, err := QueryClickHouseRows(rows, 0)
	if err != nil {
		t.Fatalf("QueryClickHouseRows: %v", err)
	}
	assertResultRows(t, result.Rows, [][]any{
		{"plain-value", "text-value", int64(42), nil, "bytes-value"},
		{"second", nil, int64(7), nil, "more-bytes"},
	})
	if !reflect.DeepEqual(result.Columns, rows.columns) {
		t.Errorf("columns = %#v, want %#v", result.Columns, rows.columns)
	}
	if !rows.closed {
		t.Error("rows were not closed")
	}
}

func TestCollectRowsNormalizesValues(t *testing.T) {
	data := [][]any{{[]byte("bytes-value"), "text-value"}}
	pos := -1
	result, err := collectRows(
		[]string{"raw", "text"},
		func() bool { pos++; return pos < len(data) },
		func() ([]any, error) {
			return []any{data[pos][0], data[pos][1]}, nil
		},
		func() error { return nil },
		func() {},
		0,
	)
	if err != nil {
		t.Fatalf("collectRows: %v", err)
	}
	assertResultRows(t, result.Rows, [][]any{{"bytes-value", "text-value"}})
}

func (f *fakeClickHouseRows) Err() error   { return f.rowErr }
func (f *fakeClickHouseRows) Close() error { f.closed = true; return nil }

func clickHouseFixture(rowErr error) *fakeClickHouseRows {
	return &fakeClickHouseRows{
		columns: []string{"id", "payload"},
		types: []chdriver.ColumnType{
			fakeColumnType{name: "id", scanType: reflect.TypeOf(int64(0))},
			fakeColumnType{name: "payload", scanType: reflect.TypeOf([]byte(nil))},
		},
		data: [][]any{
			{int64(1), []byte("alpha")},
			{int64(2), []byte("beta")},
			{int64(3), []byte("gamma")},
		},
		rowErr: rowErr,
	}
}

// The ClickHouse path now goes through collectRows, so byte values are
// normalized to strings exactly like the SQL/Postgres path (the divergence
// that caused finding 1).
func TestQueryClickHouseRowsNormalizesBytesLikeSQLPath(t *testing.T) {
	rows := clickHouseFixture(nil)

	got, err := QueryClickHouseRows(rows, 0)
	require.NoError(t, err)
	require.Equal(t, []string{"id", "payload"}, got.Columns)
	require.Len(t, got.Rows, 3)
	require.Equal(t, int64(1), got.Rows[0][0])
	require.Equal(t, "alpha", got.Rows[0][1])
	require.IsType(t, "", got.Rows[0][1])
	require.Equal(t, "gamma", got.Rows[2][1])
	require.True(t, rows.closed, "rows must be closed through the shared path")
}

// The reported defect is duplicated limit/error/close handling. Verify the
// ClickHouse path inherits the same limit semantics as collectRows.
func TestQueryClickHouseRowsHonorsSharedLimit(t *testing.T) {
	rows := clickHouseFixture(nil)

	got, err := QueryClickHouseRows(rows, 2)
	require.NoError(t, err)
	require.Len(t, got.Rows, 2)
	require.Equal(t, "beta", got.Rows[1][1])
	require.True(t, rows.closed)
}

func TestQueryClickHouseRowsPropagatesIteratedError(t *testing.T) {
	wantErr := errors.New("iteration failed")
	rows := clickHouseFixture(wantErr)

	got, err := QueryClickHouseRows(rows, 0)
	require.ErrorIs(t, err, wantErr)
	require.Empty(t, got.Rows)
	require.True(t, rows.closed)
}

func TestQueryClickHouseRowsPropagatesScanError(t *testing.T) {
	wantErr := errors.New("scan failed")
	rows := clickHouseFixture(nil)
	rows.scanErr = wantErr

	got, err := QueryClickHouseRows(rows, 0)
	require.ErrorIs(t, err, wantErr)
	require.Empty(t, got.Rows)
	require.True(t, rows.closed)
}

// Guard the shared normalization applied to every backend directly.
func TestCollectRowsNormalizesBytes(t *testing.T) {
	calls := 0
	scanner := func() ([]any, error) {
		calls++
		return []any{[]byte("value")}, nil
	}
	got, err := collectRows(
		[]string{"col"},
		func() bool { return calls < 1 },
		scanner,
		func() error { return nil },
		func() {},
		0,
	)
	require.NoError(t, err)
	require.Equal(t, "value", got.Rows[0][0])
	require.IsType(t, "", got.Rows[0][0])
}

func TestValidateQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		query   string
		args    []any
		wantErr bool
	}{
		{name: "empty", query: "", wantErr: true},
		{name: "whitespace only", query: "   \t\n ", wantErr: true},
		{name: "simple select", query: "SELECT value FROM events", wantErr: false},
		{name: "cte", query: "WITH recent AS (SELECT 1) SELECT * FROM recent", wantErr: false},
		{name: "positional postgres placeholder", query: "SELECT * FROM events WHERE name = $1", args: []any{"acme"}, wantErr: false},
		{name: "question placeholder", query: "SELECT * FROM events WHERE name = ?", args: []any{"acme"}, wantErr: false},
		{name: "named placeholder", query: "SELECT * FROM events WHERE name = @name", args: []any{"acme"}, wantErr: false},
		{name: "stacked statements", query: "SELECT 1; DROP TABLE events", wantErr: true},
		{name: "line comment", query: "SELECT 1 -- trailing", wantErr: true},
		{name: "block comment", query: "SELECT /* x */ 1", wantErr: true},
		{name: "delete", query: "DELETE FROM events", wantErr: true},
		{name: "update", query: "UPDATE events SET name = ?", args: []any{"x"}, wantErr: true},
		{name: "drop", query: "DROP TABLE events", wantErr: true},
		{name: "args without placeholder", query: "SELECT 'literal-value'", args: []any{"injected"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateQuery(tt.query, tt.args...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateQuery(%q, %d args) error = %v, wantErr %v", tt.query, len(tt.args), err, tt.wantErr)
			}
		})
	}
}
