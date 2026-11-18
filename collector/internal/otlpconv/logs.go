package otlpconv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
)

type Config struct {
	SchemaVersion  string
	EventVersion   string
	DefaultService string
}

func ConvertExportLogsRequest(req *collectorlogsv1.ExportLogsServiceRequest, cfg Config) ([][]byte, error) {
	if req == nil {
		return nil, nil
	}
	return convertResourceLogs(req.ResourceLogs, cfg)
}

func convertResourceLogs(resourceLogs []*logsv1.ResourceLogs, cfg Config) ([][]byte, error) {
	out := make([][]byte, 0)
	// Only the timestamp was used to build event_id previously, so every record
	// sharing a timestamp collided. Track how often each deterministic base ID
	// occurs within this request so even byte-identical records get unique IDs.
	idOccurrences := make(map[string]int)
	for _, resourceLog := range resourceLogs {
		resourceAttrs := kvToMap(nil, resourceLog.GetResource().GetAttributes())
		service := stringValue(resourceAttrs["service.name"])
		environment := stringValue(resourceAttrs["deployment.environment"])
		release := stringValue(resourceAttrs["service.version"])
		for _, scopeLogs := range resourceLog.GetScopeLogs() {
			for _, record := range scopeLogs.GetLogRecords() {
				attrs := kvToMap(resourceAttrs, record.GetAttributes())
				baseID := eventIDFromRecord(record, attrs)
				occurrence := idOccurrences[baseID]
				idOccurrences[baseID] = occurrence + 1
				eventID := baseID
				if occurrence > 0 {
					eventID = fmt.Sprintf("%s_%d", baseID, occurrence)
				}
				event := map[string]any{
					"schema_version": cfg.SchemaVersion,
					"event_version":  cfg.EventVersion,
					"timestamp":      timestampFromNano(record.GetTimeUnixNano(), record.GetObservedTimeUnixNano()),
					"event_id":       eventID,
					"service":        fallbackString(service, cfg.DefaultService, "otlp"),
					"event":          bodyToEventName(record.GetBody()),
					"kind":           "log",
					"level":          strings.ToLower(strings.TrimSpace(record.GetSeverityText())),
					"outcome":        outcomeFromAttrs(attrs),
					"attrs":          attrs,
					"source": map[string]any{
						"sdk": "otlp",
					},
				}
				if release != "" {
					event["release"] = release
				}
				if errors := errorsFromAttrs(attrs); len(errors) > 0 {
					event["errors"] = errors
				}
				traceFlags := traceFlagsFromRecord(record)
				if traceFlags != "" {
					event["trace_flags"] = traceFlags
				}
				if environment != "" {
					event["deployment"] = map[string]any{"environment": environment}
				}
				raw, err := json.Marshal(event)
				if err != nil {
					return nil, err
				}
				out = append(out, raw)
			}
		}
	}
	return out, nil
}

// eventIDFromRecord builds a deterministic, content-based event ID. The
// timestamp alone is not unique: batched and replayed exports frequently carry
// many records with an identical TimeUnixNano. Hashing every field that
// distinguishes a record keeps IDs stable across replays (so idempotency and
// deduplication still work) while giving distinct records distinct IDs.
func eventIDFromRecord(record *logsv1.LogRecord, attrs map[string]any) string {
	timestamp := record.GetTimeUnixNano()
	fingerprint := struct {
		TimeUnixNano         uint64         `json:"t"`
		ObservedTimeUnixNano uint64         `json:"o"`
		Body                 any            `json:"b"`
		SeverityText         string         `json:"s"`
		SeverityNumber       int32          `json:"n"`
		Flags                uint32         `json:"f"`
		Attributes           map[string]any `json:"a"`
	}{
		TimeUnixNano:         timestamp,
		ObservedTimeUnixNano: record.GetObservedTimeUnixNano(),
		Body:                 anyValue(record.GetBody()),
		SeverityText:         record.GetSeverityText(),
		SeverityNumber:       int32(record.GetSeverityNumber()),
		Flags:                record.GetFlags(),
		Attributes:           attrs,
	}
	// encoding/json emits map keys in sorted order, so equal records always
	// produce the same digest. Marshaling these primitive/map fields cannot
	// fail in practice; fall back to the old timestamp-only form if it does.
	raw, err := json.Marshal(fingerprint)
	if err != nil {
		return fmt.Sprintf("otlp_%d", timestamp)
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("otlp_%d_%s", timestamp, hex.EncodeToString(sum[:16]))
}

func outcomeFromAttrs(attrs map[string]any) string {
	for _, key := range []string{"event.outcome", "outcome"} {
		if v, ok := attrs[key].(string); ok {
			v = strings.ToLower(strings.TrimSpace(v))
			switch v {
			case "success", "error", "partial", "abandoned", "retried",
				"cancelled", "canceled", "timeout", "skipped", "rejected",
				"quarantined", "unknown":
				if v == "canceled" {
					return "cancelled"
				}
				return v
			default:
				return "unknown"
			}
		}
	}
	// Map from SeverityNumber as fallback
	return severityToOutcome(attrs)
}

func severityToOutcome(attrs map[string]any) string {
	if sevNum, ok := attrs["severity_number"]; ok {
		switch sevNum {
		case float64(9), float64(10), float64(11), float64(12):
			return "success"
		case float64(13), float64(14), float64(15), float64(16):
			return "partial"
		case float64(17), float64(18), float64(19), float64(20):
			return "error"
		case float64(21), float64(22), float64(23), float64(24):
			return "error"
		}
	}
	return "unknown"
}

func errorsFromAttrs(attrs map[string]any) []any {
	if raw, ok := attrs["event.errors"]; ok {
		if arr, ok := raw.([]any); ok {
			return arr
		}
	}
	if raw, ok := attrs["errors"]; ok {
		if arr, ok := raw.([]any); ok {
			return arr
		}
	}
	return nil
}

func traceFlagsFromRecord(record *logsv1.LogRecord) string {
	flags := record.GetFlags()
	if flags&0xFF != 0 {
		return fmt.Sprintf("%02x", flags&0xFF)
	}
	return ""
}

func kvToMap(base map[string]any, attrs []*commonv1.KeyValue) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for _, attr := range attrs {
		out[attr.GetKey()] = anyValue(attr.GetValue())
	}
	return out
}

func anyValue(v *commonv1.AnyValue) any {
	if v == nil {
		return ""
	}
	switch value := v.GetValue().(type) {
	case *commonv1.AnyValue_StringValue:
		return value.StringValue
	case *commonv1.AnyValue_BoolValue:
		return value.BoolValue
	case *commonv1.AnyValue_IntValue:
		return value.IntValue
	case *commonv1.AnyValue_DoubleValue:
		return value.DoubleValue
	case *commonv1.AnyValue_ArrayValue:
		out := make([]any, 0, len(value.ArrayValue.GetValues()))
		for _, item := range value.ArrayValue.GetValues() {
			out = append(out, anyValue(item))
		}
		return out
	case *commonv1.AnyValue_KvlistValue:
		m := map[string]any{}
		for _, item := range value.KvlistValue.GetValues() {
			m[item.GetKey()] = anyValue(item.GetValue())
		}
		return m
	case *commonv1.AnyValue_BytesValue:
		return string(value.BytesValue)
	default:
		return ""
	}
}

func timestampFromNano(primary uint64, fallback uint64) string {
	for _, value := range []uint64{primary, fallback} {
		if value == 0 {
			continue
		}
		return time.Unix(0, int64(value)).UTC().Format(time.RFC3339Nano)
	}
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func bodyToEventName(v *commonv1.AnyValue) string {
	value := anyValue(v)
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			return typed
		}
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	}
	return "otlp.log"
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func fallbackString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
