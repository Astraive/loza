package commands

import (
	"encoding/csv"
	"strings"
	"testing"
)

func TestPrintCSVEscapesSpecialCharacters(t *testing.T) {
	result := []byte(`{"columns":["id","message"],"rows":[["1","hello, world"],["2","say \"hi\"\nnext line"]]}`)

	out, err := captureStdout(t, func() error {
		return printCSV(result)
	})
	if err != nil {
		t.Fatalf("printCSV: %v", err)
	}

	records, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV output (output %q): %v", out, err)
	}

	want := [][]string{
		{"id", "message"},
		{"1", "hello, world"},
		{"2", "say \"hi\"\nnext line"},
	}
	if len(records) != len(want) {
		t.Fatalf("got %d records, want %d: %#v", len(records), len(want), records)
	}
	for i := range want {
		if len(records[i]) != len(want[i]) {
			t.Fatalf("record %d has %d columns, want %d: %#v", i, len(records[i]), len(want[i]), records[i])
		}
		for j := range want[i] {
			if records[i][j] != want[i][j] {
				t.Errorf("record %d column %d = %q, want %q", i, j, records[i][j], want[i][j])
			}
		}
	}
}
