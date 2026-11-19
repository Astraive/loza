package commands

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestComputeTotalEvents(t *testing.T) {
	tests := []struct {
		name     string
		rate     int
		duration time.Duration
		want     int
		wantErr  bool
	}{
		{
			name:     "typical run",
			rate:     100,
			duration: 10 * time.Second,
			want:     1000,
		},
		{
			name:     "sub-second duration falls back to one second",
			rate:     100,
			duration: 500 * time.Millisecond,
			want:     100,
		},
		{
			name:     "largest valid count",
			rate:     math.MaxInt,
			duration: time.Second,
			want:     math.MaxInt,
		},
		{
			name:     "overflow is rejected",
			rate:     math.MaxInt,
			duration: 2 * time.Second,
			wantErr:  true,
		},
		{
			name:     "huge duration overflow is rejected",
			rate:     math.MaxInt,
			duration: time.Duration(math.MaxInt64),
			wantErr:  true,
		},
		{
			name:     "zero rate is rejected",
			rate:     0,
			duration: 10 * time.Second,
			wantErr:  true,
		},
		{
			name:     "negative rate is rejected",
			rate:     -1,
			duration: 10 * time.Second,
			wantErr:  true,
		},
		{
			name:     "zero duration is rejected",
			rate:     100,
			duration: 0,
			wantErr:  true,
		},
		{
			name:     "negative duration is rejected",
			rate:     100,
			duration: -time.Second,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := computeTotalEvents(tt.rate, tt.duration)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("computeTotalEvents(%d, %s) = %d, want an error", tt.rate, tt.duration, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("computeTotalEvents(%d, %s): unexpected error: %v", tt.rate, tt.duration, err)
			}
			if got != tt.want {
				t.Fatalf("computeTotalEvents(%d, %s) = %d, want %d", tt.rate, tt.duration, got, tt.want)
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

// TestBenchCommandRejectsOverflow verifies the command path surfaces the
// bounds error rather than silently running with a wrapped-around event count.
func TestBenchCommandRejectsOverflow(t *testing.T) {
	cfg := config.Config{CollectorURL: "http://127.0.0.1:9308"}
	args := []string{"--rate", strconv.Itoa(math.MaxInt), "--duration", "2s"}
	if err := BenchCommand(context.Background(), cfg, args); err == nil {
		t.Fatalf("BenchCommand with overflowing rate/duration should fail")
	}
}
