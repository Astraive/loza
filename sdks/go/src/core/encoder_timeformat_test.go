package core

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// TestJSONEventEncoderTimestampFormat pins the wire representation of the
// timestamp field for every TimeFormat. In particular, TimeFormatUnixMS must
// emit an unquoted JSON integer rather than a quoted string.
func TestJSONEventEncoderTimestampFormat(t *testing.T) {
	const wantMS int64 = 1700000000123
	ts := time.UnixMilli(wantMS).UTC()

	cases := []struct {
		name   string
		format TimeFormat
		wantTS string
	}{
		{name: "rfc3339", format: TimeFormatRFC3339, wantTS: `"2023-11-14T22:13:20Z"`},
		{name: "rfc3339nano", format: TimeFormatRFC3339Nano, wantTS: `"2023-11-14T22:13:20.123Z"`},
		{name: "unix_ms", format: TimeFormatUnixMS, wantTS: `1700000000123`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := &JSONEventEncoder{TimeFormat: tc.format}
			out, err := enc.EncodeEvent(nil, &Event{Timestamp: ts})
			if err != nil {
				t.Fatalf("EncodeEvent: %v", err)
			}

			var fields map[string]json.RawMessage
			if err := json.Unmarshal(out, &fields); err != nil {
				t.Fatalf("unmarshal encoded event: %v", err)
			}
			got := string(fields["timestamp"])
			if got != tc.wantTS {
				t.Fatalf("timestamp = %s, want %s (encoded: %s)", got, tc.wantTS, out)
			}
		})
	}
}

// TestJSONEventEncoderUnixMSTimestampIsNumber explicitly asserts that the
// UnixMS timestamp decodes as a JSON number, not a string.
func TestJSONEventEncoderUnixMSTimestampIsNumber(t *testing.T) {
	ts := time.UnixMilli(1700000000123).UTC()
	enc := &JSONEventEncoder{TimeFormat: TimeFormatUnixMS}
	out, err := enc.EncodeEvent(nil, &Event{Timestamp: ts})
	if err != nil {
		t.Fatalf("EncodeEvent: %v", err)
	}

	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	var fields map[string]any
	if err := dec.Decode(&fields); err != nil {
		t.Fatalf("decode encoded event: %v", err)
	}

	num, ok := fields["timestamp"].(json.Number)
	if !ok {
		t.Fatalf("timestamp type = %T, want json.Number (encoded: %s)", fields["timestamp"], out)
	}
	if string(num) != "1700000000123" {
		t.Fatalf("timestamp = %s, want 1700000000123", num)
	}
}
