package output

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// setNoColor pins the package-level flag for the duration of a test so results
// do not depend on the NO_COLOR environment variable of the test runner.
func setNoColor(t *testing.T, v bool) {
	t.Helper()
	orig := NoColor
	NoColor = v
	t.Cleanup(func() { NoColor = orig })
}

// stripKnownCodes removes the escape sequences this package is expected to
// emit, leaving only text that came from the caller.
func stripKnownCodes(s string) string {
	for _, code := range []string{
		ColorRed, ColorGreen, ColorYellow, ColorBlue,
		ColorCyan, ColorBold, ColorDim, ColorReset,
	} {
		s = strings.ReplaceAll(s, code, "")
	}
	return s
}

// assertNoControlBytes fails if s still contains any control character or raw
// (invalid UTF-8) byte after the known wrapper codes have been removed.
func assertNoControlBytes(t *testing.T, s string) {
	t.Helper()
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			t.Fatalf("output contains raw byte 0x%02x at offset %d: %q", s[i], i, s)
		}
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			t.Fatalf("output contains control rune U+%04X at offset %d: %q", r, i, s)
		}
		i += size
	}
}

func TestColorizeStripsTerminalEscapeInjection(t *testing.T) {
	setNoColor(t, false)

	// OSC 8 hyperlink, OSC 52 clipboard write, a bare CSI sequence, an 8-bit
	// OSC introducer (0x9D), and a stray invalid UTF-8 byte (0xFF).
	payload := "safe\x1b]8;;http://evil.example\x1b\\link" +
		"\x1b]52;c;cHduZXQ=\x1b\\\x1b[2J" +
		"\x9dnot-osc\xff" + "tail"

	got := Colorize(ColorGreen, payload)
	if !strings.HasPrefix(got, ColorGreen) || !strings.HasSuffix(got, ColorReset) {
		t.Fatalf("color wrapper missing: %q", got)
	}
	assertNoControlBytes(t, stripKnownCodes(got))

	if !strings.Contains(got, "safe") || !strings.Contains(got, "tail") {
		t.Fatalf("benign text was not preserved: %q", got)
	}
}

func TestColorizeSanitizesEvenWhenNoColor(t *testing.T) {
	setNoColor(t, true)

	got := Colorize(ColorGreen, "\x1b]52;c;cHduZXQ=\x1b\\hello")
	if !strings.Contains(got, "hello") || strings.Contains(got, "\x1b") {
		t.Fatalf("expected sanitized plain text, got %q", got)
	}
	assertNoControlBytes(t, got)
}

func TestColorizePreservesUnicode(t *testing.T) {
	setNoColor(t, false)

	const text = "café 日本語 — ok"
	got := Colorize(ColorCyan, text)
	want := ColorCyan + text + ColorReset
	if got != want {
		t.Fatalf("unicode text was corrupted:\n got %q\nwant %q", got, want)
	}
}

func TestRenderASCIIGraphSanitizesUntrustedLabels(t *testing.T) {
	setNoColor(t, false)

	// This is the data flow from the finding: external graph data -> label ->
	// Bold()/Dim() -> Colorize(). The injected OSC 8 sequence must not survive.
	attack := "\x1b]8;;http://evil.example\x1b\\clickme\x1b]52;c;cHduZXQ=\x1b\\"
	graph := map[string]any{
		"nodes": []any{
			map[string]any{"id": "n1", "label": attack, "type": "service"},
			map[string]any{"id": "n2", "label": "normal"},
		},
		"edges": []any{
			map[string]any{"from_node_id": "n1", "to_node_id": "n2", "label": attack},
		},
	}

	got := RenderASCIIGraph(graph)
	assertNoControlBytes(t, stripKnownCodes(got))

	if !strings.Contains(got, "normal") || !strings.Contains(got, "SVC") {
		t.Fatalf("graph output lost benign content: %q", got)
	}
	if strings.Contains(got, "\x1b]") || strings.Contains(got, "\x1b\\") {
		t.Fatalf("OSC sequence survived sanitization: %q", got)
	}
}
