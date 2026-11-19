package postgres

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestNewRejectsInvalidTableIdentifier(t *testing.T) {
	_, err := New(context.Background(), Config{
		DSN:   "postgres://user:pass@localhost:5432/test?sslmode=disable",
		Table: "public.events;DROP TABLE x",
	})
	if err == nil {
		t.Fatalf("expected invalid table identifier error")
	}
	if !strings.Contains(err.Error(), "invalid table identifier") {
		t.Fatalf("expected invalid identifier error, got: %v", err)
	}
}

func TestNewRejectsInvalidSchemaPath(t *testing.T) {
	_, err := New(context.Background(), Config{
		DSN:   "postgres://user:pass@localhost:5432/test?sslmode=disable",
		Table: "public.events",
		Schema: map[string]string{
			"status_code": "http..status",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid schema path") {
		t.Fatalf("expected invalid schema path error, got: %v", err)
	}
}

func TestNewRejectsRawColumnConflict(t *testing.T) {
	_, err := New(context.Background(), Config{
		DSN:       "postgres://user:pass@localhost:5432/test?sslmode=disable",
		Table:     "public.events",
		RawColumn: "raw",
		StoreRaw:  true,
		Schema: map[string]string{
			"raw": "event_id",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts with schema column") {
		t.Fatalf("expected raw-column conflict error, got: %v", err)
	}
}

// Regression test for MASS-0249: sink must not carry a dead "table" field.
// The quoted table name is consumed when building rawQuery/schemaQuery in New,
// so storing it separately was unused state.
func TestSinkHasNoUnusedTableField(t *testing.T) {
	if _, ok := reflect.TypeOf(sink{}).FieldByName("table"); ok {
		t.Fatalf("sink must not carry an unused 'table' field; queries use rawQuery/schemaQuery built from the quoted table name")
	}
}

// Regression test confirming removing the field did not break query
// construction: the quoted table name still flows into the raw query.
func TestNewBuildsRawQueryFromQuotedTable(t *testing.T) {
	got, err := New(context.Background(), Config{
		DSN:   "postgres://user:pass@localhost:5432/test?sslmode=disable",
		Table: "public.events",
	})
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}
	s, ok := got.(*sink)
	if !ok {
		t.Fatalf("New() returned %T, want *sink", got)
	}
	defer s.Close(context.Background())

	want := `INSERT INTO "public"."events" ("raw") VALUES ($1)`
	if s.rawQuery != want {
		t.Fatalf("rawQuery = %q, want %q", s.rawQuery, want)
	}
}
