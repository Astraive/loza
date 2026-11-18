package jsonenc

import (
	"encoding/json"
	"testing"
)

func TestAppendEscapedStringEscapesLineAndParagraphSeparators(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"line separator", "a\u2028b", `"a\u2028b"`},
		{"paragraph separator", "a\u2029b", `"a\u2029b"`},
		{"both separators", "\u2028a\u2029", `"\u2028a\u2029"`},
		{"adjacent separators", "\u2028\u2029", `"\u2028\u2029"`},
		{"mixed multibyte", "✓\u2028é\u2029", `"✓\u2028é\u2029"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := string(AppendEscapedString(nil, tc.input))
			if got != tc.want {
				t.Fatalf("AppendEscapedString(%q) = %s, want %s", tc.input, got, tc.want)
			}
			// U+2028/U+2029 must be escaped the same way as encoding/json so
			// the output stays safe inside JavaScript string literals.
			encoded, err := json.Marshal(tc.input)
			if err != nil {
				t.Fatalf("json.Marshal(%q): %v", tc.input, err)
			}
			if got != string(encoded) {
				t.Fatalf("AppendEscapedString(%q) = %s, encoding/json = %s", tc.input, got, encoded)
			}
		})
	}
}
