package output

import (
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
)

var ansiSequence = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiSequence.ReplaceAllString(s, "")
}

func captureStdout(t *testing.T, run func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	run()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	return string(out)
}

func tableLines(t *testing.T, out string, wantCount int) []string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != wantCount {
		t.Fatalf("got %d lines, want %d: %q", len(lines), wantCount, out)
	}
	return lines
}

// assertAligned checks that every visible line has the same width, which is
// what guarantees that the header, dash rule and data columns line up.
func assertAligned(t *testing.T, lines []string) {
	t.Helper()
	want := len(lines[1])
	for i, line := range lines {
		if len(line) != want {
			t.Errorf("line %d has visible width %d, want %d: %q", i, len(line), want, line)
		}
	}
}

func TestPrintTableHeaderAlignmentWithColor(t *testing.T) {
	setNoColor(t, false) // color enabled is the default

	out := captureStdout(t, func() {
		PrintTable([]string{"Name", "Status"}, [][]string{
			{"Alice", "up"},
			{"Bob", "down"},
		})
	})
	if !strings.Contains(out, ColorBold) {
		t.Fatalf("expected bold ANSI codes (color path), got %q", out)
	}

	lines := tableLines(t, out, 4)
	stripped := make([]string, len(lines))
	for i, line := range lines {
		stripped[i] = stripANSI(line)
	}

	// Header must be padded to the plain column width (5) before it is bolded:
	// "Name " + "  " separator + "Status".
	const wantHeader = "Name   Status"
	if stripped[0] != wantHeader {
		t.Fatalf("header misaligned: got %q, want %q (raw %q)", stripped[0], wantHeader, lines[0])
	}
	assertAligned(t, stripped)
}

func TestPrintTableHeaderAlignmentWithoutColor(t *testing.T) {
	setNoColor(t, true)

	out := captureStdout(t, func() {
		PrintTable([]string{"Name", "Status"}, [][]string{
			{"Alice", "up"},
			{"Bob", "down"},
		})
	})
	if strings.Contains(out, "\033[") {
		t.Fatalf("expected no ANSI codes with NO_COLOR set, got %q", out)
	}

	lines := tableLines(t, out, 4)
	if lines[0] != "Name   Status" {
		t.Fatalf("header misaligned: got %q, want %q", lines[0], "Name   Status")
	}
	assertAligned(t, lines)
}

func TestPrintKeyValueAlignmentWithColor(t *testing.T) {
	setNoColor(t, false)

	out := captureStdout(t, func() {
		PrintKeyValue(map[string]string{
			"a":      "first",
			"longer": "second",
		})
	})
	if !strings.Contains(out, ColorDim) {
		t.Fatalf("expected dim ANSI codes (color path), got %q", out)
	}

	lines := tableLines(t, out, 2)
	want := map[string]bool{
		"  a       first":  false, // "  " + "a     " (width 6) + "  " + "first"
		"  longer  second": false,
	}
	for _, line := range lines {
		plain := stripANSI(line)
		if _, ok := want[plain]; ok {
			want[plain] = true
		}
	}
	for line, seen := range want {
		if !seen {
			t.Errorf("missing aligned line %q; got %q", line, lines)
		}
	}
}

func TestPrintKeyValueOrdersKeysDeterministically(t *testing.T) {
	pairs := map[string]string{
		"zeta":   "last",
		"alpha":  "first",
		"middle": "second",
		"beta":   "fourth",
	}

	first := captureStdout(t, func() { PrintKeyValue(pairs) })

	// Go randomizes map iteration order, so repeated calls must still produce
	// byte-identical output. This reproduces the nondeterminism before the fix.
	for i := 0; i < 25; i++ {
		got := captureStdout(t, func() { PrintKeyValue(pairs) })
		if got != first {
			t.Fatalf("PrintKeyValue output is not deterministic:\nfirst: %q\ngot:   %q", first, got)
		}
	}

	// Keys must be emitted in lexicographic order.
	wantOrder := []string{"alpha", "beta", "middle", "zeta"}
	previous := -1
	for _, key := range wantOrder {
		index := strings.Index(first, key)
		if index == -1 {
			t.Fatalf("key %q missing from output: %q", key, first)
		}
		if index <= previous {
			t.Fatalf("keys not emitted in sorted order: %q", first)
		}
		previous = index
	}
}

// Regression test for MASS-0059: a row cell with an index beyond the header
// count used to be dropped from the width pass and the render pass, while the
// "  " separator was still emitted for it, leaving a stray separator with no
// cell. All cells must now be printed, with widths sized to the longest row.
func TestPrintTableIncludesCellsBeyondHeaders(t *testing.T) {
	oldNoColor := NoColor
	NoColor = true
	defer func() { NoColor = oldNoColor }()

	out := captureStdout(t, func() {
		PrintTable(
			[]string{"H1", "H2"},
			[][]string{
				{"a", "b", "extra"},
				{"x", "y", "longer"},
			},
		)
	})

	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines (header, separator, 2 rows), got %d: %q", len(lines), out)
	}

	// Column 3 has no header, but its cells must still be rendered. Its width
	// is taken from the longest value ("longer" -> 6), and no stray trailing
	// separator is left after the last cell.
	wantRows := []string{
		"a   b   extra",
		"x   y   longer",
	}
	for i, want := range wantRows {
		got := strings.TrimRight(lines[2+i], " ")
		if got != want {
			t.Errorf("row %d: got %q, want %q", i, got, want)
		}
	}
}

// Rows that are shorter than the header count must keep the existing behavior
// (only the cells present are printed).
func TestPrintTableShorterRowsUnchanged(t *testing.T) {
	oldNoColor := NoColor
	NoColor = true
	defer func() { NoColor = oldNoColor }()

	out := captureStdout(t, func() {
		PrintTable(
			[]string{"NAME", "STATUS"},
			[][]string{{"alpha", "up"}},
		)
	})

	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header, separator, 1 row), got %d: %q", len(lines), out)
	}
	if got := strings.TrimRight(lines[2], " "); got != "alpha  up" {
		t.Errorf("row: got %q, want %q", got, "alpha  up")
	}
}
