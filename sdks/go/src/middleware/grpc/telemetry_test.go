package grpc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	loza "github.com/astraive/loza/sdks/go"
	"google.golang.org/grpc/codes"
	grpcpkg "google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// selectiveSink fails writes for failEvent and captures every other event. It
// lets a test simulate a sink failure for the canonical RPC event while still
// observing the telemetry error event emitted by the middleware.
type selectiveSink struct {
	mu        sync.Mutex
	failEvent string
	failErr   error
	captured  []*loza.Event
}

func (s *selectiveSink) Name() string { return "selective" }

func (s *selectiveSink) WriteEvent(_ context.Context, _ []byte, ev *loza.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failEvent != "" && ev.Event == s.failEvent {
		return s.failErr
	}
	s.captured = append(s.captured, ev)
	return nil
}

func (s *selectiveSink) Flush(context.Context) error { return nil }
func (s *selectiveSink) Close(context.Context) error { return nil }

func (s *selectiveSink) events() []*loza.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*loza.Event, len(s.captured))
	copy(out, s.captured)
	return out
}

func configureSelective(t *testing.T, failEvent string, failErr error) *selectiveSink {
	t.Helper()
	sink := &selectiveSink{failEvent: failEvent, failErr: failErr}
	if err := loza.Configure(loza.Test().WithSink(sink)); err != nil {
		t.Fatalf("configure: %v", err)
	}
	t.Cleanup(func() { _ = loza.Shutdown(context.Background()) })
	return sink
}

func telemetryLogs(s *selectiveSink) []*loza.Event {
	var out []*loza.Event
	for _, ev := range s.events() {
		if ev.Event == telemetryErrorEvent {
			out = append(out, ev)
		}
	}
	return out
}

func stageAttr(ev *loza.Event) string {
	for _, a := range ev.Attrs {
		if a.Key == "stage" {
			if v, ok := a.Value.(string); ok {
				return v
			}
		}
	}
	return ""
}

// TestUnaryInterceptorLogsEmitFailure verifies that a failed Emit is logged
// instead of being silently discarded, and that the RPC result is unchanged.
func TestUnaryInterceptorLogsEmitFailure(t *testing.T) {
	sinkErr := errors.New("sink down")
	sink := configureSelective(t, "grpc.request", sinkErr)
	info := &grpcpkg.UnaryServerInfo{FullMethod: "/demo.Service/Call"}

	resp, err := UnaryInterceptor()(context.Background(), "request", info,
		func(context.Context, any) (any, error) { return "response", nil })
	if err != nil || resp != "response" {
		t.Fatalf("RPC result changed by telemetry failure: resp=%v err=%v", resp, err)
	}

	found := false
	for _, ev := range telemetryLogs(sink) {
		if stageAttr(ev) == "emit" && ev.Error != nil && strings.Contains(ev.Error.Message, sinkErr.Error()) {
			found = true
		}
	}
	if !found {
		t.Fatalf("emit failure not surfaced as a telemetry log; logs=%+v", telemetryLogs(sink))
	}
}

// TestUnaryInterceptorLogsFinishFailure verifies that a failed Finish is
// logged. The handler emits the event early so the middleware's Finish fails.
func TestUnaryInterceptorLogsFinishFailure(t *testing.T) {
	sink := configureSelective(t, "", nil)
	info := &grpcpkg.UnaryServerInfo{FullMethod: "/demo.Service/Call"}

	_, err := UnaryInterceptor()(context.Background(), "request", info,
		func(ctx context.Context, _ any) (any, error) {
			if emitErr := loza.Emit(ctx); emitErr != nil {
				t.Fatalf("handler emit: %v", emitErr)
			}
			return "response", nil
		})
	if err != nil {
		t.Fatalf("unexpected RPC error: %v", err)
	}

	found := false
	for _, ev := range telemetryLogs(sink) {
		if stageAttr(ev) == "finish" {
			found = true
		}
	}
	if !found {
		t.Fatalf("finish failure not surfaced as a telemetry log; logs=%+v", telemetryLogs(sink))
	}
}

// TestStreamInterceptorLogsEmitFailure covers the same pattern on stream.go.
func TestStreamInterceptorLogsEmitFailure(t *testing.T) {
	sinkErr := errors.New("stream sink down")
	sink := configureSelective(t, "grpc.stream", sinkErr)
	info := &grpcpkg.StreamServerInfo{FullMethod: "/demo.Service/Stream"}
	stream := fakeStream{ctx: context.Background()}

	err := StreamInterceptor()(nil, stream, info, func(any, grpcpkg.ServerStream) error { return nil })
	if err != nil {
		t.Fatalf("stream RPC result changed by telemetry failure: %v", err)
	}

	found := false
	for _, ev := range telemetryLogs(sink) {
		if stageAttr(ev) == "emit" && ev.Error != nil && strings.Contains(ev.Error.Message, sinkErr.Error()) {
			found = true
		}
	}
	if !found {
		t.Fatalf("stream emit failure not surfaced as a telemetry log; logs=%+v", telemetryLogs(sink))
	}
}

func TestInterceptorsReportPanicEmitFailures(t *testing.T) {
	for _, streamMode := range []bool{false, true} {
		for _, recoverPanics := range []bool{false, true} {
			name := "unary"
			event := "grpc.request"
			if streamMode {
				name = "stream"
				event = "grpc.stream"
			}
			if recoverPanics {
				name += "/recovered"
			} else {
				name += "/propagated"
			}
			t.Run(name, func(t *testing.T) {
				sinkErr := errors.New("canonical event write failed")
				sink := &selectiveSink{failEvent: event, failErr: sinkErr}
				cfg := loza.Test().WithSink(sink).WithPanicRecovery(recoverPanics)
				if err := loza.Configure(cfg); err != nil {
					t.Fatalf("configure: %v", err)
				}
				t.Cleanup(func() { _ = loza.Shutdown(context.Background()) })

				if streamMode {
					info := &grpcpkg.StreamServerInfo{FullMethod: "/demo.Service/Stream"}
					stream := fakeStream{ctx: context.Background()}
					invoke := func() {
						err := StreamInterceptor()(nil, stream, info, func(any, grpcpkg.ServerStream) error {
							panic("fixture panic")
						})
						if recoverPanics && status.Code(err) != codes.Internal {
							t.Fatalf("recovered stream status = %v, want Internal", err)
						}
					}
					if recoverPanics {
						invoke()
					} else {
						assertPanic(t, "fixture panic", invoke)
					}
				} else {
					info := &grpcpkg.UnaryServerInfo{FullMethod: "/demo.Service/Call"}
					invoke := func() {
						_, err := UnaryInterceptor()(context.Background(), nil, info, func(context.Context, any) (any, error) {
							panic("fixture panic")
						})
						if recoverPanics && status.Code(err) != codes.Internal {
							t.Fatalf("recovered unary status = %v, want Internal", err)
						}
					}
					if recoverPanics {
						invoke()
					} else {
						assertPanic(t, "fixture panic", invoke)
					}
				}

				if err := loza.Flush(context.Background()); err != nil {
					t.Fatalf("Flush: %v", err)
				}
				logs := telemetryLogs(sink)
				if len(logs) != 1 || stageAttr(logs[0]) != "emit" || logs[0].Error == nil || !strings.Contains(logs[0].Error.Message, sinkErr.Error()) {
					t.Fatalf("panic emit diagnostics = %+v, want one emit diagnostic for %q", logs, sinkErr)
				}
			})
		}
	}
}
