package api

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/config"
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
	}
}

func TestWebSocket_ReadLimitRejectsLargeFrames(t *testing.T) {
	srv := &Server{config: &config.Config{Server: config.ServerConfig{}}}
	handler := srv.WebSocketHandler()

	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Connect via WebSocket
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
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
	}

	// The server closes the connection with 1009 (message too big) when the
	// oversized frame exceeds its read limit.
	_, _, err = conn.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
		t.Fatalf("expected read-limit close (%d), got: %v", websocket.CloseMessageTooBig, err)
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
