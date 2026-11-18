package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startFailServer is a Server whose Start immediately fails.
type startFailServer struct {
	err        error
	stopCalled atomic.Bool
}

func (s *startFailServer) Name() string                { return "fail" }
func (s *startFailServer) Addr() string                { return "127.0.0.1:0" }
func (s *startFailServer) IsReady() bool               { return false }
func (s *startFailServer) Start(context.Context) error { return s.err }
func (s *startFailServer) Stop(context.Context) error {
	s.stopCalled.Store(true)
	return nil
}

// blockingServer is a Server whose Start blocks until Stop is called. It models
// a real server (e.g. HTTP ListenAndServe) that keeps running until it is
// explicitly torn down; it neither observes stopCh nor ctx cancellation.
type blockingServer struct {
	release    chan struct{}
	once       sync.Once
	stopCalled atomic.Bool
}

func newBlockingServer() *blockingServer {
	return &blockingServer{release: make(chan struct{})}
}

func (s *blockingServer) Name() string  { return "blocking" }
func (s *blockingServer) Addr() string  { return "127.0.0.1:0" }
func (s *blockingServer) IsReady() bool { return true }
func (s *blockingServer) Start(context.Context) error {
	<-s.release
	return nil
}
func (s *blockingServer) Stop(context.Context) error {
	s.stopCalled.Store(true)
	s.once.Do(func() { close(s.release) })
	return nil
}

// TestMultiServerRunReturnsWhenServerFailsToStart reproduces the deadlock in
// MultiServer.Run's start-error branch: when one server fails to start, the
// still-running servers must be torn down so stopWG.Wait() can return.
func TestMultiServerRunReturnsWhenServerFailsToStart(t *testing.T) {
	startErr := errors.New("failed to start")
	failing := &startFailServer{err: startErr}
	blocking := newBlockingServer()

	ms := &MultiServer{
		stopCh:  make(chan struct{}),
		servers: []Server{failing, blocking},
	}

	done := make(chan error, 1)
	go func() { done <- ms.Run(context.Background()) }()

	select {
	case err := <-done:
		require.ErrorIs(t, err, startErr)
	case <-time.After(2 * time.Second):
		t.Fatal("MultiServer.Run deadlocked after a server failed to start")
	}

	assert.True(t, blocking.stopCalled.Load(), "Stop must be called on servers that are still running")
	assert.True(t, failing.stopCalled.Load(), "Stop should be called on every server during shutdown")
}
