package commands

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/astraive/loza/cli/internal/config"
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

func TestParseQueryParameterLargeIntegerKeepsPrecision(t *testing.T) {
	// 12345678901234567890 is beyond int64; ParseFloat rounds it to 12345678901234567168.
	const raw = "12345678901234567890"

	_, value, err := parseQueryParameter("id=" + raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	num, ok := value.(json.Number)
	if !ok {
		t.Fatalf("value type = %T, want json.Number preserving exact digits", value)
	}
	if num.String() != raw {
		t.Fatalf("value = %s, want %s", num.String(), raw)
	}
	encoded, err := json.Marshal(map[string]any{"value": value})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"value":`+raw) {
		t.Fatalf("marshaled value lost precision: %s", encoded)
	}
}

func TestParseQueryParameterLargeIntegerVariants(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"n=9223372036854775808", "9223372036854775808"},   // int64 max + 1
		{"n=-9223372036854775809", "-9223372036854775809"}, // int64 min - 1
		{"n=+12345678901234567890", "12345678901234567890"},
		{"n=00012345678901234567890", "12345678901234567890"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			_, value, err := parseQueryParameter(tt.raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			num, ok := value.(json.Number)
			if !ok {
				t.Fatalf("value type = %T, want json.Number", value)
			}
			if num.String() != tt.want {
				t.Fatalf("value = %s, want %s", num.String(), tt.want)
			}
		})
	}
}

func TestParseQueryParameterKeepsExistingTypes(t *testing.T) {
	tests := []struct {
		raw  string
		want any
	}{
		{"n=42", int64(42)},
		{"f=1.5", 1.5},
		{"b=true", true},
		{"z=null", nil},
		{"s=hello", "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			_, value, err := parseQueryParameter(tt.raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if !reflect.DeepEqual(value, tt.want) {
				t.Fatalf("value = %#v (%T), want %#v", value, value, tt.want)
			}
		})
	}
}

func TestQueryCommandSendsExactLargeInteger(t *testing.T) {
	const raw = "12345678901234567890"
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lql/query" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`{"columns":[],"rows":[]}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	if _, err := captureStdout(t, func() error {
		return QueryCommand(context.Background(), cfg, []string{
			"-q", "FROM events", "--param", "id=" + raw, "--format", "json",
		})
	}); err != nil {
		t.Fatalf("query: %v", err)
	}

	var payload struct {
		Parameters map[string]struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode request body %q: %v", body, err)
	}
	param, ok := payload.Parameters["id"]
	if !ok {
		t.Fatalf("missing id parameter in %s", body)
	}
	if param.Type != "int" {
		t.Fatalf("parameter type = %q, want int", param.Type)
	}
	if string(param.Value) != raw {
		t.Fatalf("wire value = %s, want %s (precision lost)", param.Value, raw)
	}
}
