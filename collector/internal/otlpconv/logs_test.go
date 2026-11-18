package otlpconv

import (
	"encoding/json"
	"testing"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
)

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

// TestSeverityNumberIntAttributeOutcome reproduces the case where a
// severity_number attribute is supplied as an OTLP integer value. anyValue
// decodes AnyValue_IntValue to int64, while severityToOutcome historically
// matched only float64, so the fallback could never match and always returned
// "unknown".
func TestSeverityNumberIntAttributeOutcome(t *testing.T) {
	tests := []struct {
		name  string
		value *commonv1.AnyValue
		want  string
	}{
		{"int info", intAttr(9), "success"},
		{"int warn", intAttr(13), "partial"},
		{"int error", intAttr(17), "error"},
		{"int fatal", intAttr(21), "error"},
		{"double info", doubleAttr(9), "success"},
		{"double warn", doubleAttr(14), "partial"},
		{"double error", doubleAttr(18), "error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &collectorlogsv1.ExportLogsServiceRequest{
				ResourceLogs: []*logsv1.ResourceLogs{
					{
						ScopeLogs: []*logsv1.ScopeLogs{{LogRecords: []*logsv1.LogRecord{{
							TimeUnixNano: 1630000000000,
							Body:         &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "log"}},
							Attributes: []*commonv1.KeyValue{{
								Key:   "severity_number",
								Value: tc.value,
							}},
						}}}},
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
			if evt["outcome"] != tc.want {
				t.Fatalf("expected outcome %q, got %v", tc.want, evt["outcome"])
			}
		})
	}
}

func TestSeverityToOutcomeNumericTypes(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"int64", int64(9), "success"},
		{"float64", float64(9), "success"},
		{"int", int(13), "partial"},
		{"int32", int32(17), "error"},
		{"fractional float", float64(9.5), "unknown"},
		{"out of range", int64(99), "unknown"},
		{"non numeric", "9", "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			attrs := map[string]any{"severity_number": tc.in}
			if got := severityToOutcome(attrs); got != tc.want {
				t.Fatalf("severityToOutcome(%v (%T)) = %q, want %q", tc.in, tc.in, got, tc.want)
			}
		})
	}
	if got := severityToOutcome(map[string]any{}); got != "unknown" {
		t.Fatalf("severityToOutcome(missing) = %q, want unknown", got)
	}
}

func intAttr(v int64) *commonv1.AnyValue {
	return &commonv1.AnyValue{Value: &commonv1.AnyValue_IntValue{IntValue: v}}
}

func doubleAttr(v float64) *commonv1.AnyValue {
	return &commonv1.AnyValue{Value: &commonv1.AnyValue_DoubleValue{DoubleValue: v}}
}
