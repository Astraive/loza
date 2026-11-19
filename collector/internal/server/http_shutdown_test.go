package server

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestHTTPServerStartFailureReleasesShutdownWatcher covers the reported defect
// that a failed ListenAndServe (e.g. address in use) left the shutdown watcher
// blocked on a long-lived context forever. Start must release the watcher when
// it returns, regardless of why it returned.
func TestHTTPServerStartFailureReleasesShutdownWatcher(t *testing.T) {
	// Occupy a port so the server under test fails to bind immediately.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	cfg := HTTPConfig{Enabled: true, Addr: ln.Addr().String()}
	srv := NewHTTPServer(cfg, &mockState{ready: true, healthy: true})

	// A long-lived context: without the fix the watcher would leak here.
	err = srv.Start(context.Background())
	require.Error(t, err, "expected ListenAndServe to fail on an occupied address")

	// Start has returned, so the watcher's exit signal must be in place.
	select {
	case <-srv.done:
	case <-time.After(time.Second):
		t.Fatal("shutdown watcher was not released after Start returned")
	}
}

// TestHTTPServerStopWithConcurrentStart covers the unsynchronized server
// pointer: Stop may be called while Start is installing it. Run with -race to
// catch unsynchronized access; the test also asserts that Start always returns.
func TestHTTPServerStopWithConcurrentStart(t *testing.T) {
	cfg := HTTPConfig{Enabled: true, Addr: "127.0.0.1:0"}
	srv := NewHTTPServer(cfg, &mockState{ready: true, healthy: true})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	// Stop can overlap Start; it must be safe even if the server is not set yet.
	_ = srv.Stop(context.Background())
	cancel() // guarantee shutdown even if Stop ran before Start installed the server

	select {
	case <-errCh:
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after shutdown")
	}
}
