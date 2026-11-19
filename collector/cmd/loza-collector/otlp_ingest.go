package main

import (
	"io"
	"net/http"
	"strings"

	"github.com/astraive/loza/collector/internal/otlpconv"
	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// handleOTLPLogs converts an OTLP/HTTP logs request into collector events.
// Detailed errors are logged server-side; clients only receive stable error
// codes and messages so internal implementation and infrastructure details
// (for example "dedupe unavailable: ...") are never reflected back to them.
func (s *collectorState) handleOTLPLogs(w http.ResponseWriter, r *http.Request) {
	s.metrics.requestsTotal.Add(1)
	if !s.isAuthorized(r) {
		s.metrics.requestsAuthErr.Add(1)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "auth_failed"})
		return
	}
	cfg := s.config()
	body, err := io.ReadAll(io.LimitReader(r.Body, cfg.maxBodyBytes))
	if err != nil {
		logJSON("warn", "collector_otlp_body_read_failed", map[string]any{"error": err.Error()})
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_otlp_request", "message": "failed to read OTLP request body"})
		return
	}

	req := &collectorlogsv1.ExportLogsServiceRequest{}
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	switch {
	case strings.Contains(contentType, "application/x-protobuf"), strings.Contains(contentType, "application/protobuf"):
		if err := proto.Unmarshal(body, req); err != nil {
			logJSON("warn", "collector_otlp_protobuf_decode_failed", map[string]any{"error": err.Error()})
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_otlp_protobuf", "message": "invalid OTLP protobuf payload"})
			return
		}
	default:
		if err := protojson.Unmarshal(body, req); err != nil {
			logJSON("warn", "collector_otlp_json_decode_failed", map[string]any{"error": err.Error()})
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_otlp_json", "message": "invalid OTLP JSON payload"})
			return
		}
	}

	events, err := otlpconv.ConvertExportLogsRequest(req, otlpconv.Config{
		SchemaVersion:  cfg.schemaSchemaVersion,
		EventVersion:   cfg.schemaEventVersion,
		DefaultService: cfg.boundServiceName,
	})
	if err != nil {
		logJSON("error", "collector_otlp_conversion_failed", map[string]any{"error": err.Error()})
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "otlp_conversion_failed", "message": "failed to convert OTLP payload"})
		return
	}
	accepted, err := s.handleIngestBatch(r.Context(), events)
	if err != nil {
		logJSON("error", "collector_otlp_ingest_failed", map[string]any{"error": err.Error()})
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "otlp_ingest_failed", "message": "OTLP ingestion temporarily unavailable"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"partialSuccess": map[string]any{}, "accepted": accepted})
}
