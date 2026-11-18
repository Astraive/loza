package storagepath

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
