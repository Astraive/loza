package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"
)

func TestLogAuditEmitsTimestamp(t *testing.T) {
	var buf bytes.Buffer
	logger := NewSlogAuditLogger(slog.New(slog.NewJSONHandler(&buf, nil)))

	ts := time.Date(2026, time.November, 19, 12, 34, 56, 0, time.UTC)
	logger.LogAudit(context.Background(), AuditEvent{
		Timestamp: ts,
		Event:     AuditKeyAuthenticated,
		KeyID:     "key-1",
	})

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("log output is not valid JSON: %v (%s)", err, buf.String())
	}

	raw, ok := record["timestamp"]
	if !ok {
		t.Fatalf("audit log is missing the timestamp attribute: %s", buf.String())
	}
	value, ok := raw.(string)
	if !ok {
		t.Fatalf("timestamp attribute has unexpected type %T", raw)
	}
	got, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("timestamp %q is not RFC3339Nano: %v", value, err)
	}
	if !got.Equal(ts) {
		t.Errorf("timestamp = %v, want %v", got, ts)
	}
}
