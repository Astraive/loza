package utils_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/astraive/loza/sdks/go/src/utils"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestParseNDJSONSkipsBlankLinesAndReportsMalformedLine(t *testing.T) {
	events, err := utils.ParseNDJSON([]byte("\n {\"event\":\"ok\"} \n\n"))
	if err != nil {
		t.Fatalf("parse blank lines: %v", err)
	}
	if len(events) != 1 || events[0]["event"] != "ok" {
		t.Fatalf("events = %#v", events)
	}
	_, err = utils.ParseNDJSON([]byte("{\"ok\":true}\nnot-json\n"))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("malformed error = %v", err)
	}
}

func TestParseNDJSONRejectsNullLine(t *testing.T) {
	cases := []struct {
		payload  string
		wantLine string
	}{
		{"null", "line 1"},
		{"{\"event\":\"ok\"}\nnull\n{\"event\":\"last\"}", "line 2"},
	}
	for _, tc := range cases {
		events, err := utils.ParseNDJSON([]byte(tc.payload))
		if err == nil || !strings.Contains(err.Error(), "expected JSON object") || !strings.Contains(err.Error(), tc.wantLine) {
			t.Fatalf("null line error for %q = %v", tc.payload, err)
		}
		if events != nil {
			t.Fatalf("null line should return no events, got %#v", events)
		}
	}
}

func TestParseNDJSONReaderReportsReadError(t *testing.T) {
	if _, err := utils.ParseNDJSONReader(failingReader{}); err == nil || !strings.Contains(err.Error(), "read ndjson") {
		t.Fatalf("read error = %v", err)
	}
}
