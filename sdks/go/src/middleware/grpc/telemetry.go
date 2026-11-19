package grpc

import (
	"context"

	"github.com/astraive/loza/sdks/go"
)

// telemetryErrorEvent is the stable event name used when the middleware fails
// to finish or emit the canonical event for an RPC.
const telemetryErrorEvent = "grpc.telemetry_error"

// reportTelemetryError leaves an observable signal when finishing or emitting
// a canonical event fails. Telemetry loss must never change the RPC result, so
// the error is logged instead of propagated, but it must not be silently
// discarded with `_ =` either.
func reportTelemetryError(ctx context.Context, stage string, err error) {
	if err == nil {
		return
	}
	loza.ErrorContext(ctx, "grpc telemetry failure", err, telemetryErrorEvent,
		loza.String("stage", stage),
	)
}
