package grpc

import (
	"context"
	"fmt"

	"github.com/astraive/loza/sdks/go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}

// StreamInterceptor starts and emits canonical events around stream handlers.
func StreamInterceptor(cfg ...Config) grpc.StreamServerInterceptor {
	c := Config{}
	if len(cfg) > 0 {
		c = cfg[0]
	}
	return StreamInterceptorWithConfig(c)
}

// StreamInterceptorWithConfig starts and emits canonical events around stream handlers.
func StreamInterceptorWithConfig(cfg Config) grpc.StreamServerInterceptor {
	eventName := cfg.Event
	if eventName == "" {
		eventName = "grpc.stream"
	}

	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		evCtx := loza.StartEvent(ss.Context(), loza.Params{
			Event:  eventName,
			Method: "grpc",
			Route:  info.FullMethod,
			Path:   info.FullMethod,
		})
		recoverPanics := loza.PanicRecoveryEnabled()

		wrapped := &wrappedServerStream{ServerStream: ss, ctx: evCtx}

		// Finalize and emit on every exit path, including panic unwinding, so a
		// panic in the handler never silently drops the started event.
		defer func() {
			if rec := recover(); rec != nil {
				if !recoverPanics {
					loza.FinishError(evCtx, status.Error(codes.Internal, fmt.Sprintf("panic: %v", rec)), loza.Int("status_code", int(codes.Internal)))
					_ = loza.Emit(evCtx)
					panic(rec)
				}
				// Record the panic value in server-side telemetry only. gRPC
				// transmits the status message to the caller, so clients must
				// receive a fixed, non-sensitive message instead.
				loza.FinishError(evCtx, fmt.Errorf("panic recovered: %v", rec), loza.Int("status_code", int(codes.Internal)))
				_ = loza.Emit(evCtx)
				err = status.Error(codes.Internal, "internal server error")
				return
			}
		if !loza.PanicRecoveryEnabled() {
			defer func() {
				if rec := recover(); rec != nil {
					// The panic is intentionally propagated to the caller, but the
					// event started above must still be finished and emitted so it
					// is not orphaned when recovery is disabled.
					loza.FinishError(evCtx, fmt.Errorf("panic: %v", rec), loza.Int("status_code", int(codes.Unknown)))
					_ = loza.Emit(evCtx)
					panic(rec)
				}
			}()
			err := handler(srv, wrapped)
			if err != nil {
				st, _ := status.FromError(err)
				reportTelemetryError(evCtx, "finish_error", loza.FinishError(evCtx, err, loza.Int("status_code", int(st.Code()))))
			} else {
				reportTelemetryError(evCtx, "finish", loza.Finish(evCtx, "success", loza.Int("status_code", int(codes.OK))))
			}
			_ = loza.Emit(evCtx)
		}()

		err = handler(srv, wrapped)
			reportTelemetryError(evCtx, "emit", loza.Emit(evCtx))
			return err
		}

		defer func() {
			if rec := recover(); rec != nil {
				err = status.Error(codes.Internal, fmt.Sprintf("panic recovered: %v", rec))
				reportTelemetryError(evCtx, "finish_error", loza.FinishError(evCtx, err, loza.Int("status_code", int(codes.Internal))))
				reportTelemetryError(evCtx, "emit", loza.Emit(evCtx))
			}
		}()

		err = handler(srv, wrapped)
		if err != nil {
			st, _ := status.FromError(err)
			reportTelemetryError(evCtx, "finish_error", loza.FinishError(evCtx, err, loza.Int("status_code", int(st.Code()))))
		} else {
			reportTelemetryError(evCtx, "finish", loza.Finish(evCtx, "success", loza.Int("status_code", int(codes.OK))))
		}
		reportTelemetryError(evCtx, "emit", loza.Emit(evCtx))
		return err
	}
}
