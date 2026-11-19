package loza_test

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/astraive/loza/sdks/go"
)

func TestHTTPUserAgentTruncatesOnRuneBoundary(t *testing.T) {
	// A three-byte rune starts at byte 511, so byte 512 lands in the middle
	// of it. A raw byte slice would produce invalid UTF-8 here.
	ua := strings.Repeat("a", 511) + "€" + strings.Repeat("b", 100)

	attr := loza.HTTPUserAgent(ua)
	got, ok := attr.Value.(string)
	if !ok {
		t.Fatalf("expected string value, got %T", attr.Value)
	}
	if attr.Key != "http.user_agent" {
		t.Fatalf("unexpected key %q", attr.Key)
	}
	if len(got) > 512 {
		t.Fatalf("value length %d exceeds 512 bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncated value is not valid UTF-8: %q", got)
	}
	if want := strings.Repeat("a", 511); got != want {
		t.Fatalf("expected truncation before the straddling rune, got %d bytes", len(got))
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("truncated value contains U+FFFD replacement rune")
	}

	// The reported impact is a replacement rune appearing after JSON encoding.
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	if strings.Contains(string(encoded), "\ufffd") {
		t.Fatalf("JSON encoding contains U+FFFD: %s", encoded)
	}
}

func TestHTTPUserAgentTruncatesASCITo512Bytes(t *testing.T) {
	attr := loza.HTTPUserAgent(strings.Repeat("x", 600))
	got, ok := attr.Value.(string)
	if !ok {
		t.Fatalf("expected string value, got %T", attr.Value)
	}
	if want := strings.Repeat("x", 512); got != want {
		t.Fatalf("expected exactly 512 bytes, got %d", len(got))
	}
}

func TestHTTPUserAgentLeavesShortValuesUntouched(t *testing.T) {
	ua := "Mozilla/5.0 (🙂) LozaTest"
	attr := loza.HTTPUserAgent(ua)
	if got := attr.Value.(string); got != ua {
		t.Fatalf("expected %q, got %q", ua, got)
	}
}
