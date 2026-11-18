package otlpconv

import (
	"encoding/json"
	"testing"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
)

func TestConvertExportLogsRequestUniqueEventIDsForSharedTimestamp(t *testing.T) {
	// Two distinct records that share the same nanosecond timestamp must not
	// receive the same event_id: downstream uses event_id as an idempotency key,
	// so a collision would cause a legitimate event to be dropped as a duplicate.
	newRecord := func(body string, attrs ...*commonv1.KeyValue) *logsv1.LogRecord {
		return &logsv1.LogRecord{
			TimeUnixNano: 1630000000000,
			Body:         &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: body}},
			SeverityText: "INFO",
			Attributes:   attrs,
		}
	}
	kv := func(k, v string) *commonv1.KeyValue {
		return &commonv1.KeyValue{Key: k, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: v}}}
	}

	req := &collectorlogsv1.ExportLogsServiceRequest{
		ResourceLogs: []*logsv1.ResourceLogs{{
			Resource: &resourcev1.Resource{Attributes: []*commonv1.KeyValue{
				{Key: "service.name", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "checkout"}}},
			}},
			ScopeLogs: []*logsv1.ScopeLogs{{LogRecords: []*logsv1.LogRecord{
				newRecord("order.created", kv("order_id", "a")),
				newRecord("order.shipped", kv("order_id", "b")),
				newRecord("order.created", kv("order_id", "a")), // byte-identical to the first
				newRecord("order.created", kv("order_id", "c")),
			}}},
		}},
	}

	out, err := ConvertExportLogsRequest(req, Config{SchemaVersion: "v1", EventVersion: "v1", DefaultService: "default"})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if len(out) != 4 {
		t.Fatalf("expected 4 events, got %d", len(out))
	}

	seen := map[string]int{}
	for i, raw := range out {
		var evt map[string]interface{}
		if err := json.Unmarshal(raw, &evt); err != nil {
			t.Fatalf("event %d unmarshal: %v", i, err)
		}
		id, ok := evt["event_id"].(string)
		if !ok || id == "" {
			t.Fatalf("event %d missing event_id: %v", i, evt["event_id"])
		}
		seen[id]++
	}
	if len(seen) != len(out) {
		t.Fatalf("event_id collision: %d unique IDs for %d events: %v", len(seen), len(out), seen)
	}
}

func TestConvertExportLogsRequestEventIDIsStableForIdenticalRecord(t *testing.T) {
	// Event IDs must stay deterministic so a replayed export dedupes correctly.
	record := &logsv1.LogRecord{
		TimeUnixNano:         1630000000000,
		ObservedTimeUnixNano: 1630000000001,
		Body:                 &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "order.created"}},
		SeverityText:         "INFO",
		Attributes: []*commonv1.KeyValue{
			{Key: "order_id", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "a"}}},
		},
	}
	build := func(records []*logsv1.LogRecord) string {
		req := &collectorlogsv1.ExportLogsServiceRequest{
			ResourceLogs: []*logsv1.ResourceLogs{{
				ScopeLogs: []*logsv1.ScopeLogs{{LogRecords: records}},
			}},
		}
		out, err := ConvertExportLogsRequest(req, Config{SchemaVersion: "v1", EventVersion: "v1"})
		if err != nil {
			t.Fatalf("convert error: %v", err)
		}
		var evt map[string]interface{}
		if err := json.Unmarshal(out[0], &evt); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return evt["event_id"].(string)
	}

	first := build([]*logsv1.LogRecord{record})
	replay := build([]*logsv1.LogRecord{record})
	if first != replay {
		t.Fatalf("event_id is not stable for identical records: %q != %q", first, replay)
	}
}

func TestConvertExportLogsRequestBasic(t *testing.T) {
	req := &collectorlogsv1.ExportLogsServiceRequest{
		ResourceLogs: []*logsv1.ResourceLogs{
			{
				Resource:  &resourcev1.Resource{Attributes: []*commonv1.KeyValue{{Key: "service.name", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "checkout"}}}}},
				ScopeLogs: []*logsv1.ScopeLogs{{LogRecords: []*logsv1.LogRecord{{TimeUnixNano: 1630000000000, Body: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "order.created"}}, SeverityText: "INFO"}}}},
			},
		},
	}
	out, err := ConvertExportLogsRequest(req, Config{SchemaVersion: "1", EventVersion: "1", DefaultService: "default"})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 event, got %d", len(out))
	}
	var evt map[string]interface{}
	if err := json.Unmarshal(out[0], &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if evt["service"] != "checkout" {
		t.Fatalf("expected service checkout, got %v", evt["service"])
	}
	if evt["event"] != "order.created" {
		t.Fatalf("expected event name, got %v", evt["event"])
	}
}
