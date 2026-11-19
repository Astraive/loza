package grpc

import (
	"context"
	"fmt"

	"github.com/astraive/loza/sdks/go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Config controls gRPC middleware behavior.
type Config struct {
	Event string
}

// UnaryInterceptor starts and emits canonical events around unary handlers.
func UnaryInterceptor(cfg ...Config) grpc.UnaryServerInterceptor {
	c := Config{}
	if len(cfg) > 0 {
		c = cfg[0]
	}
	return UnaryInterceptorWithConfig(c)
}

// UnaryInterceptorWithConfig starts and emits canonical events around unary handlers.
func UnaryInterceptorWithConfig(cfg Config) grpc.UnaryServerInterceptor {
	eventName := cfg.Event
	if eventName == "" {
		eventName = "grpc.request"
	}

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		evCtx := loza.StartEvent(ctx, loza.Params{
			Event:  eventName,
			Method: "grpc",
			Route:  info.FullMethod,
			Path:   info.FullMethod,
		})
		recoverPanics := loza.PanicRecoveryEnabled()

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
			resp, err := handler(evCtx, req)
			if err != nil {
				st, _ := status.FromError(err)
				loza.FinishError(evCtx, err, loza.Int("status_code", int(st.Code())))
			} else {
				loza.Finish(evCtx, "success", loza.Int("status_code", int(codes.OK)))
			}
			_ = loza.Emit(evCtx)
		}()

		resp, err = handler(evCtx, req)
		return resp, err
	}
}
