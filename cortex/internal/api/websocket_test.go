package api

import (
	"errors"
	"fmt"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/config"
	"github.com/astraive/loza/cortex/internal/middleware"
	transportcontracts "github.com/astraive/loza/spec/transport/contracts"
	"github.com/gorilla/websocket"
)

// fataler is the subset of testing.TB used by sendFrameOrFail. It exists so the
// write-error escalation can be exercised with a stub in a unit test.
type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

// sendFrameOrFail writes a WebSocket frame and hard-fails the test if the write
// fails. An oversized-frame test must never pass without its payload actually
// reaching the server, so a write error is escalated instead of being logged
// and ignored.
func sendFrameOrFail(t fataler, write func(int, []byte) error, messageType int, data []byte) {
	t.Helper()
	if err := write(messageType, data); err != nil {
		t.Fatalf("write websocket frame: %v", err)
// readerRoleContext returns an authenticated context whose role is below writer.
func readerRoleContext() context.Context {
	return middleware.WithAuthResult(context.Background(), &middleware.AuthResult{
		Authorized: true,
		Role:       "reader",
	})
}

// ingest_batch is an HTTP route action (POST /events/batch), never a WebSocket
// action. It must not pass through the WebSocket write-role gate: doing so made
// the authorization branch dead code and reported a misleading error to clients.
func TestExecuteWebSocketAction_IngestBatchIsNotRoleGated(t *testing.T) {
	srv := &Server{}

	_, err := srv.executeWebSocketAction(readerRoleContext(), transportcontracts.WebSocketRequest{Action: "ingest_batch"})
	if err == nil {
		t.Fatal("expected ingest_batch to be rejected as an unknown websocket action")
	}
	if !strings.Contains(err.Error(), `unknown websocket action "ingest_batch"`) {
		t.Fatalf("expected unknown-action error, got %v", err)
	}
}

// Removing the dead ingest_batch gate must not weaken the gate on the real
// WebSocket write action.
func TestExecuteWebSocketAction_IngestEventStillRequiresWriterRole(t *testing.T) {
	srv := &Server{}

	_, err := srv.executeWebSocketAction(readerRoleContext(), transportcontracts.WebSocketRequest{Action: "ingest_event"})
	if err == nil {
		t.Fatal("expected ingest_event from a reader role to be rejected")
	}
	if !strings.Contains(err.Error(), "writer role required for ingest operations") {
		t.Fatalf("expected writer-role error, got %v", err)
	}
// isReadLimitCloseError reports whether err is the CloseMessageTooBig close
// error the server sends when a frame exceeds its configured read limit.
func isReadLimitCloseError(err error) bool {
	return websocket.IsCloseError(err, websocket.CloseMessageTooBig)
}

func TestWebSocket_ReadLimitRejectsLargeFrames(t *testing.T) {
	srv := &Server{config: &config.Config{Server: config.ServerConfig{}}}
	handler := srv.WebSocketHandler()

	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Connect via WebSocket. Use a write buffer larger than the 1MB read limit
	// so the oversized payload is sent as a single frame; with the default 4KB
	// buffer the message is fragmented and the read limit is never exercised.
	dialer := *websocket.DefaultDialer
	dialer.WriteBufferSize = 2 << 20
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send a well-formed message exceeding the 1MB read limit (1MB + 1 byte).
	// Keeping the JSON valid means that without the read limit the server would
	// accept the request and reply normally, so a rejection can only come from
	// the read limit. A failed write is a hard failure, never a silent return.
	bigPayload := strings.Repeat("x", 1<<20+1)
	sendFrameOrFail(t, conn.WriteMessage, websocket.TextMessage,
		[]byte(`{"action":"healthz","event":"`+bigPayload+`"}`))

	// Bound the read so a missing rejection fails fast instead of hanging.
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	err = conn.WriteMessage(websocket.TextMessage, []byte(`{"action":"healthz","event":`+bigPayload+`}`))
	if err != nil {
		// The server closes the connection as soon as it reads the oversized
		// frame header, so the client may be mid-write when the socket is torn
		// down. The close error read below is the authoritative signal.
		t.Logf("write interrupted by server close: %v", err)
	}

	// The server closes the connection with 1009 (message too big) when the
	// oversized frame exceeds its read limit.
	_, _, err = conn.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
		t.Fatalf("expected read-limit close (%d), got: %v", websocket.CloseMessageTooBig, err)
	if err == nil {
		t.Fatal("expected connection to be closed due to read limit violation")
	}
	// Require the specific close code for an oversized message so an unrelated
	// error (timeout, reset, protocol error) cannot pass this test.
	if !isReadLimitCloseError(err) {
		t.Fatalf("expected CloseMessageTooBig close error from read limit violation, got: %v", err)
	}
}

func TestIsReadLimitCloseError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"no error", nil, false},
		{"message too big", &websocket.CloseError{Code: websocket.CloseMessageTooBig}, true},
		{"normal closure", &websocket.CloseError{Code: websocket.CloseNormalClosure}, false},
		{"abnormal closure", &websocket.CloseError{Code: websocket.CloseAbnormalClosure}, false},
		{"unrelated error", errors.New("connection reset by peer"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isReadLimitCloseError(tt.err); got != tt.want {
				t.Fatalf("isReadLimitCloseError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestWebSocket_ReadLimitAllowsSmallFrames(t *testing.T) {
	srv := &Server{config: &config.Config{Server: config.ServerConfig{}}}
	handler := srv.WebSocketHandler()

	ts := httptest.NewServer(handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send a small message — should succeed
	err = conn.WriteMessage(websocket.TextMessage, []byte(`{"action":"healthz"}`))
	if err != nil {
		t.Fatalf("write small message: %v", err)
	}

	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if len(msg) == 0 {
		t.Fatal("expected non-empty response")
	}
}

type recordingFataler struct {
	fatals []string
}

func (r *recordingFataler) Helper() {}

func (r *recordingFataler) Fatalf(format string, args ...any) {
	r.fatals = append(r.fatals, fmt.Sprintf(format, args...))
}

// TestSendFrameOrFail_WriteErrorIsFatal is a regression test for the vacuous
// pass at websocket_test.go:30. A failed frame write must escalate as a hard
// failure; with the old log-and-return behavior no failure was recorded and the
// oversized-frame test passed without ever exercising the server's read limit.
func TestSendFrameOrFail_WriteErrorIsFatal(t *testing.T) {
	sentinel := errors.New("send buffer too small")

	t.Run("write error is reported as fatal", func(t *testing.T) {
		stub := &recordingFataler{}
		writeCalled := false
		sendFrameOrFail(stub, func(int, []byte) error {
			writeCalled = true
			return sentinel
		}, websocket.TextMessage, []byte("payload"))

		if !writeCalled {
			t.Fatal("expected the frame write to be attempted")
		}
		if len(stub.fatals) != 1 {
			t.Fatalf("expected exactly one fatal on write error, got %d", len(stub.fatals))
		}
		if !strings.Contains(stub.fatals[0], sentinel.Error()) {
			t.Fatalf("fatal %q does not mention the write error", stub.fatals[0])
		}
	})

	t.Run("successful write is not fatal", func(t *testing.T) {
		stub := &recordingFataler{}
		sendFrameOrFail(stub, func(int, []byte) error {
			return nil
		}, websocket.TextMessage, []byte("payload"))

		if len(stub.fatals) != 0 {
			t.Fatalf("expected no fatal on successful write, got %v", stub.fatals)
		}
	})
}
