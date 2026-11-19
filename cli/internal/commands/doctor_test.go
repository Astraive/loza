package commands

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/astraive/loza/cli/internal/config"
)

// Regression for MASS-0028: DoctorCommand accepted ctx but every client call
// used its own context.Background(), so canceling the caller's context could
// not stop an in-flight health check.
func TestDoctorCommandStopsHealthChecksOnCanceledContext(t *testing.T) {
	entered := make(chan struct{})
	var enteredOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			enteredOnce.Do(func() { close(entered) })
			select {
			case <-r.Context().Done():
			case <-time.After(3 * time.Second):
			}
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Config{
		CollectorRepoPath: t.TempDir(),
		SpecRepoPath:      t.TempDir(),
		CollectorURL:      srv.URL,
		DuckDBPath:        filepath.Join(t.TempDir(), "doctor.db"),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type result struct {
		err error
	}
	done := make(chan result, 1)
	go func() {
		done <- result{err: DoctorCommand(ctx, cfg, []string{"--metrics=false"})}
	}()

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("doctor never reached the blocking health check")
	}
	cancel()

	select {
	case res := <-done:
		if !errors.Is(res.err, context.Canceled) {
			t.Fatalf("expected canceled context error, got %v", res.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("DoctorCommand did not stop after the caller canceled the context")
	}
}
