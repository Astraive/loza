package nats

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/astraive/loza/collector/internal/eventbus"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// countingMsg records acknowledgments so tests can assert that exactly one
// owner (the handler) drives them.
type countingMsg struct {
	data []byte
	acks int
	naks int
}

func (m *countingMsg) Metadata() (*jetstream.MsgMetadata, error) { return nil, nil }
func (m *countingMsg) Data() []byte                              { return m.data }
func (m *countingMsg) Headers() nats.Header                      { return nil }
func (m *countingMsg) Subject() string                           { return "loza.test.events" }
func (m *countingMsg) Reply() string                             { return "" }
func (m *countingMsg) Ack() error                                { m.acks++; return nil }
func (m *countingMsg) DoubleAck(context.Context) error           { m.acks++; return nil }
func (m *countingMsg) Nak() error                                { m.naks++; return nil }
func (m *countingMsg) NakWithDelay(time.Duration) error          { m.naks++; return nil }
func (m *countingMsg) InProgress() error                         { return nil }
func (m *countingMsg) Term() error                               { return nil }
func (m *countingMsg) TermWithReason(string) error               { return nil }

var _ jetstream.Msg = (*countingMsg)(nil)

func ackTestMsg(t *testing.T, id string) *countingMsg {
	t.Helper()
	data, err := json.Marshal(eventbus.Envelope{
		ID: id, Event: "test.event", Timestamp: time.Now(), Body: []byte(`{"x":1}`),
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return &countingMsg{data: data}
}

// TestHandleMessageHandlerOwnsAck reproduces the reported conflict: the
// adapter must not ack/nack based on the handler's return value because the
// handler already owns acknowledgment through Message.Ack/Message.Nack.
func TestHandleMessageHandlerOwnsAck(t *testing.T) {
	tests := []struct {
		name     string
		handler  func(context.Context, eventbus.Message) error
		wantAcks int
		wantNaks int
	}{
		{
			name: "handler acks and returns nil is acked once",
			handler: func(ctx context.Context, msg eventbus.Message) error {
				return msg.Ack(ctx)
			},
			wantAcks: 1,
			wantNaks: 0,
		},
		{
			name: "handler nacks and returns nil is not acked",
			handler: func(ctx context.Context, msg eventbus.Message) error {
				_ = msg.Nack(ctx, errors.New("handler nack"))
				return nil
			},
			wantAcks: 0,
			wantNaks: 1,
		},
		{
			name: "handler acks and returns error is not nacked",
			handler: func(ctx context.Context, msg eventbus.Message) error {
				_ = msg.Ack(ctx)
				return errors.New("downstream failure")
			},
			wantAcks: 1,
			wantNaks: 0,
		},
		{
			name:     "handler returning nil without ack is not acked by adapter",
			handler:  func(context.Context, eventbus.Message) error { return nil },
			wantAcks: 0,
			wantNaks: 0,
		},
		{
			name:     "handler returning error without nack is not nacked by adapter",
			handler:  func(context.Context, eventbus.Message) error { return errors.New("failure") },
			wantAcks: 0,
			wantNaks: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := ackTestMsg(t, "nats-ack-1")
			handleMessage(context.Background(), msg, "loza.test.events", tc.handler)
			if msg.acks != tc.wantAcks || msg.naks != tc.wantNaks {
				t.Fatalf("ack/nack counts = (%d, %d), want (%d, %d)",
					msg.acks, msg.naks, tc.wantAcks, tc.wantNaks)
			}
		})
	}
}

// TestHandleMessageDeferredAck mirrors the worker reader: the handler returns
// nil after enqueueing, and the message is acked only when the downstream
// commit closure runs. The adapter must not ack early.
func TestHandleMessageDeferredAck(t *testing.T) {
	msg := ackTestMsg(t, "nats-ack-deferred")
	var captured eventbus.Message

	handleMessage(context.Background(), msg, "loza.test.events",
		func(_ context.Context, m eventbus.Message) error {
			captured = m
			return nil
		})

	if msg.acks != 0 || msg.naks != 0 {
		t.Fatalf("adapter acked before commit: acks=%d naks=%d", msg.acks, msg.naks)
	}
	if captured == nil {
		t.Fatal("handler did not receive a message")
	}
	if got := captured.ID(); got != "nats-ack-deferred" {
		t.Fatalf("message ID = %q, want %q", got, "nats-ack-deferred")
	}
	if got := captured.Topic(); got != "loza.test.events" {
		t.Fatalf("message topic = %q, want %q", got, "loza.test.events")
	}

	if err := captured.Ack(context.Background()); err != nil {
		t.Fatalf("deferred ack: %v", err)
	}
	if msg.acks != 1 || msg.naks != 0 {
		t.Fatalf("after commit ack/nack counts = (%d, %d), want (1, 0)", msg.acks, msg.naks)
	}
}

func TestHandleMessageMalformedEnvelopeNacks(t *testing.T) {
	msg := &countingMsg{data: []byte("not-json")}
	called := false
	handleMessage(context.Background(), msg, "loza.test.events",
		func(context.Context, eventbus.Message) error {
			called = true
			return nil
		})

	if called {
		t.Fatal("handler called for malformed envelope")
	}
	if msg.acks != 0 || msg.naks != 1 {
		t.Fatalf("ack/nack counts = (%d, %d), want (0, 1)", msg.acks, msg.naks)
	}
}
