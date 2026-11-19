package query

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// fakeRows is a rowScanner that can report an error from Err() after all rows
// have been consumed, simulating a mid-stream scan/IO failure in database/sql.
type fakeRows struct {
	columns []string
	rows    [][]any
	err     error
	scanErr error
	idx     int
}

func (f *fakeRows) Columns() ([]string, error) { return f.columns, nil }

func (f *fakeRows) Next() bool {
	if f.idx >= len(f.rows) {
		return false
	}
	f.idx++
	return true
}

func (f *fakeRows) Scan(dest ...any) error {
	if f.scanErr != nil {
		return f.scanErr
	}
	row := f.rows[f.idx-1]
	if len(dest) != len(row) {
		return fmt.Errorf("got %d destinations, want %d", len(dest), len(row))
	}
	for i, d := range dest {
		p, ok := d.(*any)
		if !ok {
			return fmt.Errorf("destination %d is %T, want *any", i, d)
		}
		*p = row[i]
	}
	return nil
}

func (f *fakeRows) Err() error { return f.err }

// captureOutput keeps printRows' stdout writes from cluttering test output.
func captureOutput(t *testing.T, fn func()) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	os.Stdout = old
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close pipe reader: %v", err)
	}
}

func TestPrintRowsSurfacesIterationError(t *testing.T) {
	iterErr := errors.New("mid-stream read failure")
	fake := &fakeRows{
		columns: []string{"id", "name"},
		rows:    [][]any{{int64(1), "alpha"}},
		err:     iterErr,
	}

	var err error
	captureOutput(t, func() { err = printRows(fake) })

	if err == nil {
		t.Fatal("printRows() returned nil; want the error reported by rows.Err()")
	}
	if !errors.Is(err, iterErr) {
		t.Fatalf("printRows() error = %v; want wrapped %v", err, iterErr)
	}
	if !strings.Contains(err.Error(), "iterate rows") {
		t.Fatalf("printRows() error = %q; want 'iterate rows' context", err.Error())
	}
}

func TestPrintRowsCleanIteration(t *testing.T) {
	fake := &fakeRows{
		columns: []string{"id", "name"},
		rows:    [][]any{{int64(1), "alpha"}, {int64(2), "beta"}},
	}
	captureOutput(t, func() {
		if err := printRows(fake); err != nil {
			t.Errorf("printRows() unexpected error: %v", err)
		}
	})
}

func TestPrintRowsSurfacesScanError(t *testing.T) {
	scanErr := errors.New("scan failed")
	fake := &fakeRows{
		columns: []string{"id"},
		rows:    [][]any{{int64(1)}},
		scanErr: scanErr,
	}

	var err error
	captureOutput(t, func() { err = printRows(fake) })

	if !errors.Is(err, scanErr) {
		t.Fatalf("printRows() error = %v; want wrapped %v", err, scanErr)
	}
}
