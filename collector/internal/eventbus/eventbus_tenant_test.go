package eventbus_test

import (
	"context"
	"testing"

	"github.com/astraive/loza/collector/internal/eventbus"
)

// capturingBus records envelopes published through the SinkAdapter without
// requiring an external broker.
type capturingBus struct {
	published []eventbus.Envelope
}

func (b *capturingBus) Publish(_ context.Context, _ string, events []eventbus.Envelope) error {
	b.published = append(b.published, events...)
	return nil
}

func (b *capturingBus) Subscribe(context.Context, string, string, eventbus.Handler) error { return nil }
func (b *capturingBus) Close(context.Context) error                                       { return nil }
func (b *capturingBus) Health(context.Context) eventbus.Health                            { return eventbus.Health{OK: true} }

// TestSinkAdapterPopulatesTenantID guards MASS-0201: ExtractTenantID used to be
// dead code with no production callers. The eventbus routing adapter now uses
// it to populate Envelope.TenantID from the event payload.
func TestSinkAdapterPopulatesTenantID(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name string
		body string
		want string
	}{
		{"top-level tenant_id", `{"event":"a","tenant_id":"tenant-123"}`, "tenant-123"},
		{"nested tenant.id", `{"event":"a","tenant":{"id":"tenant-456"}}`, "tenant-456"},
		{"missing tenant", `{"event":"a","service":"checkout"}`, ""},
		{"invalid json", `not json`, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bus := &capturingBus{}
			adapter := eventbus.NewSinkAdapter(bus, "tenant-routing")
			if err := adapter.WriteEvent(ctx, []byte(c.body), nil); err != nil {
				t.Fatalf("WriteEvent: %v", err)
			}
			if len(bus.published) != 1 {
				t.Fatalf("expected 1 envelope, got %d", len(bus.published))
			}
			if got := bus.published[0].TenantID; got != c.want {
				t.Fatalf("TenantID: expected %q, got %q", c.want, got)
			}
			if string(bus.published[0].Body) != c.body {
				t.Fatalf("Body was modified: %q", bus.published[0].Body)
			}
		})
	}
}

// TestSinkAdapterWriteBatchPopulatesTenantID covers the batch path as well.
func TestSinkAdapterWriteBatchPopulatesTenantID(t *testing.T) {
	bus := &capturingBus{}
	adapter := eventbus.NewSinkAdapter(bus, "tenant-routing")
	bodies := [][]byte{
		[]byte(`{"tenant_id":"tenant-a"}`),
		[]byte(`{"tenant":{"id":"tenant-b"}}`),
	}
	if err := adapter.WriteBatch(context.Background(), bodies); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if len(bus.published) != 2 {
		t.Fatalf("expected 2 envelopes, got %d", len(bus.published))
	}
	want := []string{"tenant-a", "tenant-b"}
	for i, env := range bus.published {
		if env.TenantID != want[i] {
			t.Fatalf("envelope %d: expected TenantID %q, got %q", i, want[i], env.TenantID)
		}
	}
}
