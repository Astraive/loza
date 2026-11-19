package commands

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/astraive/loza/cli/internal/config"
	"github.com/astraive/loza/cli/internal/output"
)

const streamTestLine = `{"level":"error","timestamp":"2026-01-02T03:04:05Z","service":"checkout","event":"payment.failed","release":"v1.2.3","trace_flags":"01","errors":[{"code":"E1"}]}`

func withoutColor(t *testing.T) {
	t.Helper()
	old := output.NoColor
	output.NoColor = true
	t.Cleanup(func() { output.NoColor = old })
}

// TestWatchAndTailShareEventFormatting is the regression for the duplicated
// scanner/JSON/level-colouring logic: both commands must render the common
// columns identically, with extra fields supplied only through options.
func TestWatchAndTailShareEventFormatting(t *testing.T) {
	withoutColor(t)

	watchOut, err := captureStdout(t, func() error {
		return streamEvents(strings.NewReader(streamTestLine+"\n"), streamOptions{})
	})
	if err != nil {
		t.Fatalf("watch-style stream: %v", err)
	}

	tailOut, err := captureStdout(t, func() error {
		return streamEvents(strings.NewReader(streamTestLine+"\n"), streamOptions{extraFields: tailExtraFields})
	})
	if err != nil {
		t.Fatalf("tail-style stream: %v", err)
	}

	wantBase := "2026-01-02T03:04:05Z error checkout payment.failed"
	if watchOut != wantBase+"\n" {
		t.Fatalf("watch output = %q, want %q", watchOut, wantBase+"\n")
	}
	if !strings.HasPrefix(tailOut, wantBase+" ") {
		t.Fatalf("tail output = %q, want prefix %q", tailOut, wantBase+" ")
	}
	for _, extra := range []string{"release=v1.2.3", "trace_flags=01", `errors=[{"code":"E1"}]`} {
		if !strings.Contains(tailOut, extra) {
			t.Fatalf("tail output %q missing %q", tailOut, extra)
		}
		if strings.Contains(watchOut, extra) {
			t.Fatalf("watch output %q should not contain %q", watchOut, extra)
		}
	}
}

// TestStreamEventsPropagatesScannerError verifies that the shared helper keeps
// the error-handling behaviour both commands rely on (truncated streams are
// not silently treated as a clean exit).
func TestStreamEventsPropagatesScannerError(t *testing.T) {
	withoutColor(t)

	wantErr := errors.New("stream truncated")
	for _, tc := range []struct {
		name string
		opts streamOptions
	}{
		{name: "watch"},
		{name: "tail", opts: streamOptions{ctx: context.Background(), extraFields: tailExtraFields}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &errReader{data: []byte(streamTestLine + "\n"), err: wantErr}
			_, err := captureStdout(t, func() error {
				return streamEvents(r, tc.opts)
			})
			if !errors.Is(err, wantErr) {
				t.Fatalf("streamEvents error = %v, want %v", err, wantErr)
			}
		})
	}
}

// TestStreamEventsHonorsContextCancellation covers the ctx-handling parameter:
// a cancelled context stops streaming without producing output or an error.
func TestStreamEventsHonorsContextCancellation(t *testing.T) {
	withoutColor(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := captureStdout(t, func() error {
		return streamEvents(strings.NewReader(streamTestLine+"\n"), streamOptions{
			ctx:         ctx,
			extraFields: tailExtraFields,
		})
	})
	if err != nil {
		t.Fatalf("streamEvents with cancelled ctx: %v", err)
	}
	if out != "" {
		t.Fatalf("streamEvents with cancelled ctx printed %q, want nothing", out)
	}
}

// TestStreamEventsEchoesNonJSONLines verifies both option sets preserve the
// shared fallback for malformed input while skipping blank lines.
func TestStreamEventsEchoesNonJSONLines(t *testing.T) {
	withoutColor(t)

	input := "plain text line\n\n" + streamTestLine + "\n"
	for _, tc := range []struct {
		name string
		opts streamOptions
	}{
		{name: "watch"},
		{name: "tail", opts: streamOptions{ctx: context.Background(), extraFields: tailExtraFields}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := captureStdout(t, func() error {
				return streamEvents(strings.NewReader(input), tc.opts)
			})
			if err != nil {
				t.Fatalf("streamEvents: %v", err)
			}
			if !strings.HasPrefix(out, "plain text line\n") {
				t.Fatalf("output = %q, want echoed non-JSON line first", out)
			}
			if !strings.Contains(out, "payment.failed") {
				t.Fatalf("output = %q, want formatted JSON event", out)
			}
		})
	}
}

// TestWatchAndTailCommandsUseSharedFormatter checks the wiring end to end:
// both commands consume the same stream and render identical common columns.
func TestWatchAndTailCommandsUseSharedFormatter(t *testing.T) {
	withoutColor(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tail" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(streamTestLine + "\n"))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	watchOut, err := captureStdout(t, func() error {
		return WatchCommand(context.Background(), cfg, nil)
	})
	if err != nil {
		t.Fatalf("WatchCommand: %v", err)
	}
	tailOut, err := captureStdout(t, func() error {
		return TailCommand(context.Background(), cfg, nil)
	})
	if err != nil {
		t.Fatalf("TailCommand: %v", err)
	}

	wantBase := "2026-01-02T03:04:05Z error checkout payment.failed"
	if !strings.HasSuffix(watchOut, wantBase+"\n") {
		t.Fatalf("watch output = %q, want suffix %q", watchOut, wantBase+"\n")
	}
	if !strings.Contains(tailOut, wantBase+" ") || !strings.Contains(tailOut, "release=v1.2.3") {
		t.Fatalf("tail output = %q, want shared columns plus extra fields", tailOut)
	}
}
