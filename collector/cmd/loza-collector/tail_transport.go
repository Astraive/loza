package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	serverruntime "github.com/astraive/loza/collector/internal/server"
	duckdbsink "github.com/astraive/loza/collector/internal/sinks/duckdb"
	publichttp "github.com/astraive/loza/collector/server/http"
	transportcontracts "github.com/astraive/loza/spec/transport/contracts"
	"github.com/gorilla/websocket"
)

// The tail WebSocket feature is not yet wired into the router.
// Keeping the implementation for future use.
//lint:ignore U1000 tail transport

//nolint:unused
var tailWSUpgrader = websocket.Upgrader{
	ReadBufferSize:  16 * 1024,
	WriteBufferSize: 16 * 1024,
	CheckOrigin: func(r *http.Request) bool {
		return serverruntime.NewWebSocketUpgrader(nil).CheckOrigin(r)
	},
}

//nolint:unused
func (s *collectorState) handleTailWebSocket(w http.ResponseWriter, r *http.Request) {
	if !s.isAuthorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "auth_failed"})
		return
	}
	filters, err := serverruntime.ParseTailFilters(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	conn, err := tailWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(1 * 1024 * 1024) // 1MB max frame

	// The connection is hijacked, so r.Context() is not reliably cancelled when
	// the client disconnects. Read from the connection to observe close frames
	// and read errors, and signal the main loop to release its subscriber.
	clientClosed := make(chan struct{})
	go func() {
		defer close(clientClosed)
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}()

	if err := s.streamHistoricalTail(r.Context(), filters, func(raw []byte) error {
		return s.writeTailWebSocketEvent(conn, raw)
	}); err != nil {
		_ = conn.WriteJSON(transportcontracts.WebSocketResponse{Type: "error", Error: err.Error()})
		return
	}

	ch := make(chan []byte, 128)
	s.addTailSubscriber(ch)
	defer s.removeTailSubscriber(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case <-clientClosed:
			return
		case raw, ok := <-ch:
			if !ok {
				return
			}
			if !s.TailMatches(r.Context(), raw, filters) {
				continue
			}
			if err := s.writeTailWebSocketEvent(conn, raw); err != nil {
				return
			}
		}
	}
}

//nolint:unused
func (s *collectorState) streamHistoricalTail(ctx context.Context, filters serverruntime.TailFilters, write func([]byte) error) error {
	rows, err := s.queryTailHistory(ctx, filters)
	if err != nil {
		return err
	}
	for _, raw := range rows {
		if err := write(raw); err != nil {
			return err
		}
	}
	return nil
}

func (s *collectorState) queryTailHistory(ctx context.Context, filters serverruntime.TailFilters) ([][]byte, error) {
	filters = tailFiltersWithAuthorizedScope(ctx, filters)
	if s.queryDB == nil {
		return nil, nil
	}
	rawIdent, err := quoteSQLIdent(s.config().duckDBRawColumn)
	if err != nil {
		return nil, err
	}
	tableIdent, err := quoteSQLIdent(s.config().duckDBTable)
	if err != nil {
		return nil, err
	}
	tsColumn, err := s.tailTimestampColumn()
	if err != nil {
		return nil, err
	}
	tsIdent, err := quoteSQLIdent(tsColumn)
	if err != nil {
		return nil, err
	}
	if encryptionEnabled(s.config().storageEncryptionKey) {
		return s.queryEncryptedTailHistory(ctx, filters, rawIdent, tableIdent, tsIdent)
	}
	idExpr := fmt.Sprintf("coalesce(json_extract_string(%s, '$.event_id'), json_extract_string(%s, '$.id'), '')", rawIdent, rawIdent)
	conds := make([]string, 0, 8)
	if !filters.Since.IsZero() {
		since := quoteSQLString(filters.Since.UTC().Format(time.RFC3339Nano))
		if filters.AfterEventID != "" {
			conds = append(conds, fmt.Sprintf("(%s > TIMESTAMP %s OR (%s = TIMESTAMP %s AND %s > %s))", tsIdent, since, tsIdent, since, idExpr, quoteSQLString(filters.AfterEventID)))
		} else {
			conds = append(conds, fmt.Sprintf("%s >= TIMESTAMP %s", tsIdent, since))
		}
	} else if filters.AfterEventID != "" {
		conds = append(conds, fmt.Sprintf("%s > %s", idExpr, quoteSQLString(filters.AfterEventID)))
	}
	if filters.Collector != "" {
		conds = append(conds, fmt.Sprintf("json_extract_string(%s, '$.collector') = %s", rawIdent, quoteSQLString(filters.Collector)))
	}
	if filters.Environment != "" {
		conds = append(conds, fmt.Sprintf("json_extract_string(%s, '$.environment') = %s", rawIdent, quoteSQLString(filters.Environment)))
	}
	if filters.Service != "" {
		conds = append(conds, fmt.Sprintf("json_extract_string(%s, '$.service') = %s", rawIdent, quoteSQLString(filters.Service)))
	}
	if filters.Kind != "" {
		conds = append(conds, fmt.Sprintf("json_extract_string(%s, '$.kind') = %s", rawIdent, quoteSQLString(filters.Kind)))
	}
	if filters.TraceID != "" {
		conds = append(conds, fmt.Sprintf("json_extract_string(%s, '$.trace_id') = %s", rawIdent, quoteSQLString(filters.TraceID)))
	}
	if filters.IncidentID != "" {
		conds = append(conds, fmt.Sprintf("json_extract_string(%s, '$.incident_id') = %s", rawIdent, quoteSQLString(filters.IncidentID)))
	}

	query := fmt.Sprintf("SELECT %s FROM %s", rawIdent, tableIdent)
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += fmt.Sprintf(" ORDER BY %s ASC, %s ASC LIMIT %d", tsIdent, idExpr, filters.Limit)

	rows, err := s.queryDB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out [][]byte
	for rows.Next() {
		raw, err := scanRawRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, rows.Err()
}

func (s *collectorState) queryEncryptedTailHistory(
	ctx context.Context,
	filters serverruntime.TailFilters,
	rawIdent string,
	tableIdent string,
	tsIdent string,
) ([][]byte, error) {
	query := fmt.Sprintf("SELECT %s FROM %s ORDER BY %s ASC", rawIdent, tableIdent, tsIdent)
	rows, err := s.queryDB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([][]byte, 0, filters.Limit)
	for rows.Next() {
		encrypted, err := scanRawRow(rows)
		if err != nil {
			return nil, err
		}
		raw, err := duckdbsink.DecryptRaw(encrypted, s.config().storageEncryptionKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt tail history: %w", err)
		}
		if !rawMatchesTailCursor(raw, filters) || !rawMatchesTailFilters(raw, filters) {
			continue
		}
		out = append(out, raw)
		if len(out) == filters.Limit {
			break
		}
	}
	return out, rows.Err()
}

func rawMatchesTailCursor(raw []byte, filters serverruntime.TailFilters) bool {
	if filters.Since.IsZero() && filters.AfterEventID == "" {
		return true
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	eventID := stringValue(payload["event_id"])
	if eventID == "" {
		eventID = stringValue(payload["id"])
	}
	if filters.Since.IsZero() {
		return eventID > filters.AfterEventID
	}
	eventTime, err := time.Parse(time.RFC3339Nano, stringValue(payload["timestamp"]))
	if err != nil || eventTime.Before(filters.Since) {
		return false
	}
	return !eventTime.Equal(filters.Since) || filters.AfterEventID == "" || eventID > filters.AfterEventID
}

// tailTimestampColumn resolves the single column projected from the "timestamp"
// path. Map iteration order in Go is randomized, so returning the first match
// makes ORDER BY/WHERE selection nondeterministic when a (mis)configuration maps
// more than one column to that path. Resolve deterministically and surface the
// ambiguity as an error instead of silently picking a varying column.
func (s *collectorState) tailTimestampColumn() (string, error) {
	var candidates []string
	for col, path := range s.config().duckDBSchema {
		if path == "timestamp" {
			candidates = append(candidates, col)
		}
	}
	switch len(candidates) {
	case 0:
		return "timestamp", nil
	case 1:
		return candidates[0], nil
	default:
		sort.Strings(candidates)
		return "", fmt.Errorf("ambiguous timestamp column: %d columns map to the timestamp path (%s)", len(candidates), strings.Join(candidates, ", "))
	}
}

func scanRawRow(rows *sql.Rows) ([]byte, error) {
	var rawValue any
	if err := rows.Scan(&rawValue); err != nil {
		return nil, err
	}
	switch typed := rawValue.(type) {
	case string:
		return []byte(typed), nil
	case []byte:
		return append([]byte(nil), typed...), nil
	default:
		return nil, fmt.Errorf("unsupported raw row type %T", rawValue)
	}
}

func rawMatchesTailFilters(raw []byte, filters serverruntime.TailFilters) bool {
	return serverruntime.RawMatchesTailFilters(raw, filters)
}

func writeNDJSONFrame(w *bufio.Writer, raw []byte) error {
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		if err := w.WriteByte('\n'); err != nil {
			return err
		}
	}
	return w.Flush()
}

func streamHistoryToWriter(ctx context.Context, writer io.Writer, rows [][]byte) error {
	bw := bufio.NewWriter(writer)
	for _, raw := range rows {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := writeNDJSONFrame(bw, raw); err != nil {
			return err
		}
	}
	return nil
}

func (s *collectorState) TailHistory(ctx context.Context, filters serverruntime.TailFilters) ([][]byte, error) {
	return s.queryTailHistory(ctx, filters)
}

func (s *collectorState) TailMatches(ctx context.Context, raw []byte, filters serverruntime.TailFilters) bool {
	return rawMatchesTailFilters(raw, tailFiltersWithAuthorizedScope(ctx, filters))
}

func tailFiltersWithAuthorizedScope(ctx context.Context, filters serverruntime.TailFilters) serverruntime.TailFilters {
	if scope, ok := publichttp.AuthorizedCollectorFromContext(ctx); ok {
		filters.Collector = scope.Name
		filters.Environment = scope.Environment
	}
	return filters
}

func (s *collectorState) AddTailSubscriber(ch chan []byte) {
	s.addTailSubscriber(ch)
}

func (s *collectorState) RemoveTailSubscriber(ch chan []byte) {
	s.removeTailSubscriber(ch)
}

//nolint:unused
func (s *collectorState) writeTailWebSocketEvent(conn *websocket.Conn, raw []byte) error {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return conn.WriteMessage(websocket.TextMessage, raw)
	}
	return conn.WriteJSON(transportcontracts.WebSocketResponse{
		Type: "event",
		Data: payload,
	})
}
