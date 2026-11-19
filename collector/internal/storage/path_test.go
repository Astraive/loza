package storage

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNDJSONArchiveKey(t *testing.T) {
	ts := time.Date(2026, time.March, 9, 14, 5, 6, 123456789, time.UTC)
	got := NDJSONArchiveKey("archive/raw", ts)
	want := "archive/raw/dataset=events/date=2026-03-09/hour=14/part-1773065106123456789.ndjson"
	if got != want {
		t.Fatalf("unexpected key:\nwant: %s\ngot:  %s", want, got)
	}
}

func TestParquetArchiveKeySanitizesTable(t *testing.T) {
	ts := time.Date(2026, time.March, 9, 14, 5, 6, 123456789, time.UTC)
	got := ParquetArchiveKey("", `analytics\wide.events`, ts)
	want := "dataset=analytics_wide_events/date=2026-03-09/hour=14/part-1773065106123456789.parquet"
	if got != want {
		t.Fatalf("unexpected key:\nwant: %s\ngot:  %s", want, got)
	}
}

func TestNormalizePrefixRejectsTraversal(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		want   string
	}{
		{"parent segments dropped", "../..", ""},
		{"backslash parent segments dropped", `..\..\secret`, "secret/"},
		{"embedded parent segment dropped", "tenant/../admin", "tenant/admin/"},
		{"leading parent segments dropped", "../../dataset", "dataset/"},
		{"current segment dropped", "a/./b", "a/b/"},
		{"empty segment dropped", "a//b", "a/b/"},
		{"query delimiter segment dropped", "tenant/raw?x=1", "tenant/"},
		{"fragment delimiter segment dropped", "tenant/raw#frag", "tenant/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizePrefix(tt.prefix); got != tt.want {
				t.Fatalf("normalizePrefix(%q) = %q, want %q", tt.prefix, got, tt.want)
			}
		})
	}
}

func TestArchiveKeyNeverContainsTraversal(t *testing.T) {
	ts := time.Date(2026, time.March, 9, 14, 5, 6, 123456789, time.UTC)
	got := NDJSONArchiveKey("../..", ts)
	if strings.Contains(got, "..") {
		t.Fatalf("archive key still contains traversal: %q", got)
	}
	if !strings.HasPrefix(got, "dataset=events/") {
		t.Fatalf("archive key does not start at dataset segment: %q", got)
	}
}

func TestLocalParquetExportPath(t *testing.T) {
	ts := time.Date(2026, time.March, 9, 14, 5, 6, 123456789, time.UTC)
	got := filepath.ToSlash(LocalParquetExportPath("exports", "analytics.events", ts))
	want := "exports/table=analytics_events/date=2026-03-09/hour=14/part-1773065106123456789.parquet"
	if got != want {
		t.Fatalf("unexpected path:\nwant: %s\ngot:  %s", want, got)
	}
}

func TestNormalizePrefixStripsTraversalSegments(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"../..", ""},
		{`..\..\`, ""},
		{"archive/../raw", "archive/raw/"},
		{"/archive/./raw/", "archive/raw/"},
		{"a//b", "a/b/"},
		{"", ""},
		{"archive/raw", "archive/raw/"},
	}
	for _, tt := range tests {
		if got := normalizePrefix(tt.in); got != tt.want {
			t.Errorf("normalizePrefix(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestArchiveKeyHasNoTraversalSegments(t *testing.T) {
	ts := time.Date(2026, time.March, 9, 14, 5, 6, 123456789, time.UTC)
	got := NDJSONArchiveKey("../..", ts)
	want := "dataset=events/date=2026-03-09/hour=14/part-1773065106123456789.ndjson"
	if got != want {
		t.Fatalf("unexpected key with traversal prefix:\nwant: %s\ngot:  %s", want, got)
	}
}

func TestArchiveKeyOutsideUnixNanoRange(t *testing.T) {
	// Year 2300 is past the int64-nanosecond limit (~2262): UnixNano wraps to a
	// negative, undefined value. The part name must still encode the exact
	// nanosecond count so distinct instants cannot share a key.
	future := time.Date(2300, time.March, 9, 14, 5, 6, 123456789, time.UTC)
	if future.UnixNano() >= 0 {
		t.Fatalf("precondition: expected UnixNano overflow for year 2300, got %d", future.UnixNano())
	}
	want := "archive/raw/dataset=events/date=2300-03-09/hour=14/part-10419631506123456789.ndjson"
	if got := NDJSONArchiveKey("archive/raw", future); got != want {
		t.Fatalf("unexpected future key:\nwant: %s\ngot:  %s", want, got)
	}

	// The same must hold before ~1678, where UnixNano also wraps.
	past := time.Date(1600, time.March, 9, 14, 5, 6, 123456789, time.UTC)
	if past.UnixNano() <= 0 {
		t.Fatalf("precondition: expected UnixNano wrap for year 1600, got %d", past.UnixNano())
	}
	want = "archive/raw/dataset=events/date=1600-03-09/hour=14/part--11670170093876543211.ndjson"
	if got := NDJSONArchiveKey("archive/raw", past); got != want {
		t.Fatalf("unexpected past key:\nwant: %s\ngot:  %s", want, got)
	}
}

func TestLocalParquetExportPathOutsideUnixNanoRange(t *testing.T) {
	future := time.Date(2300, time.March, 9, 14, 5, 6, 123456789, time.UTC)
	got := filepath.ToSlash(LocalParquetExportPath("exports", "analytics.events", future))
	want := "exports/table=analytics_events/date=2300-03-09/hour=14/part-10419631506123456789.parquet"
	if got != want {
		t.Fatalf("unexpected future path:\nwant: %s\ngot:  %s", want, got)
	}
}

func TestArchiveKeyPreservesNanosecondPrecision(t *testing.T) {
	base := time.Date(2300, time.March, 9, 14, 5, 6, 123456789, time.UTC)
	a := NDJSONArchiveKey("archive/raw", base)
	b := NDJSONArchiveKey("archive/raw", base.Add(1*time.Nanosecond))
	if a == b {
		t.Fatalf("timestamps one nanosecond apart produced the same key: %s", a)
	}
	if a == NDJSONArchiveKey("archive/raw", base.Add(-1*time.Nanosecond)) {
		t.Fatalf("timestamps one nanosecond apart produced the same key: %s", a)
	}
}
