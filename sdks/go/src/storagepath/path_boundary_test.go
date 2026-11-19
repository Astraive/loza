package storagepath

import (
	"testing"
	"time"
)

func TestStoragePathSanitizationDefaults(t *testing.T) {
	if got := normalizePrefix(" /archive\\raw/ "); got != "archive/raw/" {
		t.Fatalf("normalize prefix = %q", got)
	}
	if got := normalizePrefix("///"); got != "" {
		t.Fatalf("empty prefix = %q", got)
	}
	if got := sanitizeSegment(" "); got != "events" {
		t.Fatalf("empty segment = %q", got)
	}
	if got := sanitizeExt(" "); got != "ndjson" {
		t.Fatalf("empty extension = %q", got)
	}
	if got := sanitizeExt(".parquet\\"); got != "parquet" {
		t.Fatalf("sanitized extension = %q", got)
	}
	_ = NDJSONArchiveKey("", time.Unix(0, 0))
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
