package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/astraive/loza/cli/internal/config"
)

func TestBenchTotalEvents(t *testing.T) {
	tests := []struct {
		name     string
		rate     int
		duration time.Duration
		want     int
	}{
		{"sub-second duration", 100, 500 * time.Millisecond, 50},
		{"fractional seconds", 100, 10500 * time.Millisecond, 1050},
		{"whole seconds", 100, 10 * time.Second, 1000},
		{"rounds half away from zero", 3, 1500 * time.Millisecond, 5},
		{"zero duration falls back to rate", 100, 0, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := benchTotalEvents(tt.rate, tt.duration); got != tt.want {
				t.Fatalf("benchTotalEvents(%d, %v) = %d, want %d", tt.rate, tt.duration, got, tt.want)
			}
		})
	}
}

func TestBenchCommandSubSecondDurationSendsCorrectEventCount(t *testing.T) {
	var gotEvents int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Events []json.RawMessage `json:"events"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		atomic.AddInt64(&gotEvents, int64(len(body.Events)))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	_, err := captureStdout(t, func() error {
		return BenchCommand(context.Background(), cfg, []string{
			"--url", srv.URL,
			"--rate", "100",
			"--duration", "500ms",
		})
	})
	if err != nil {
		t.Fatalf("bench command: %v", err)
	}
	if got := atomic.LoadInt64(&gotEvents); got != 50 {
		t.Fatalf("sent %d events for 100 events/sec over 500ms, want 50", got)
	}
}
