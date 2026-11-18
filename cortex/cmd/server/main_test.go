package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/config"
	"github.com/astraive/loza/cortex/internal/models"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

type noopBatchProcessor struct{}

func (noopBatchProcessor) ProcessBatch(context.Context, []*models.Event) error { return nil }

func TestStartCollectorSyncReturnsWaitableCompletion(t *testing.T) {
	cfg := config.Default()
	cfg.Collector.SourceOfTruth = true
	cfg.Collector.Mode = "pull"

	ctx, cancel := context.WithCancel(context.Background())
	done := startCollectorSync(ctx, cfg.Collector, noopBatchProcessor{})
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("collector sync did not stop after cancellation")
	}
}

func TestShutdownGRPCServerStopsLateHandoff(t *testing.T) {
	// Reproduces the reported defect: shutdown begins before the server is
	// handed off. The fix must still reach and stop the server rather than
	// observing a nil global and silently skipping graceful shutdown.
	ready := make(chan *grpc.Server, 1)
	srv := grpc.NewServer()

	go func() {
		time.Sleep(20 * time.Millisecond)
		ready <- srv
	}()

	shutdownGRPCServer(ready)

	// A stopped server rejects Serve with ErrServerStopped.
	lis := bufconn.Listen(1024)
	if err := srv.Serve(lis); !errors.Is(err, grpc.ErrServerStopped) {
		t.Fatalf("gRPC server was not stopped after late handoff: got %v", err)
	}
}

func TestShutdownGRPCServerNoServerReturnsPromptly(t *testing.T) {
	// A setup failure closes the channel without a server; shutdown must not
	// block waiting for one that will never arrive.
	ready := make(chan *grpc.Server)
	close(ready)

	done := make(chan struct{})
	go func() {
		shutdownGRPCServer(ready)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked waiting for a server that will not arrive")
	}
}

func TestShutdownGRPCServerNilChannel(t *testing.T) {
	// A nil channel means gRPC is disabled; shutdown is a no-op.
	done := make(chan struct{})
	go func() {
		shutdownGRPCServer(nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown with gRPC disabled should be a no-op")
	}
}

func TestStartGRPCServerClosesChannelOnSetupFailure(t *testing.T) {
	// TLS setup fails before a listener or server is created. The returned
	// channel must close without a value so callers never block on it.
	cfg := config.Default()
	cfg.TLS.Enabled = true
	cfg.TLS.CertFile = "/nonexistent/loza-cert.pem"
	cfg.TLS.KeyFile = "/nonexistent/loza-key.pem"

	ready := startGRPCServer(cfg, nil)
	select {
	case srv, ok := <-ready:
		if ok {
			srv.Stop()
			t.Fatal("unexpected gRPC server handed off despite TLS setup failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("startGRPCServer did not signal setup failure via channel close")
	}
}
