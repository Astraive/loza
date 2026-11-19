package commands

import (
	"net/url"
	"strings"
	"testing"
)

func TestNormalizeFetchURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"http", "http://example.com/events", false},
		{"https with special chars", "https://example.com/events?q=a b&x=1 #frag", false},
		{"file scheme", "file:///etc/passwd", true},
		{"javascript scheme", "javascript:alert(1)", true},
		{"ftp scheme", "ftp://example.com/events", true},
		{"missing host", "http:///events", true},
		{"not a url", "example.com/events", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeFetchURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got URL %q", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			parsed, err := url.Parse(got)
			if err != nil {
				t.Fatalf("normalized URL %q does not parse: %v", got, err)
			}
			if parsed.Scheme != "http" && parsed.Scheme != "https" {
				t.Errorf("unexpected scheme %q in %q", parsed.Scheme, got)
			}
		})
	}
}

func TestNormalizeFetchURLEncodesSpecialCharacters(t *testing.T) {
	raw := "https://example.com/events?q=a b&x=1"
	got, err := normalizeFetchURL(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, " ") {
		t.Errorf("normalized URL still has a raw space: %q", got)
	}
	if !strings.Contains(got, "q=a+b") {
		t.Errorf("space was not encoded in the query: %q", got)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse normalized URL: %v", err)
	}
	if q := parsed.Query().Get("q"); q != "a b" {
		t.Errorf("query q = %q, want %q", q, "a b")
	}
}
