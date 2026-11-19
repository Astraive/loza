package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/astraive/loza/sdks/go"
	"github.com/astraive/loza/sdks/go/src/sinks/httpbatch"
)

// deliverAndShutdown emits the event and then drains the sinks. Both returned
// errors are surfaced to the caller instead of being silently discarded. Emit
// runs before Shutdown to preserve the ordering of the previous defers.
func deliverAndShutdown(ctx context.Context, emit, shutdown func(context.Context) error) error {
	var errs []error
	if err := emit(ctx); err != nil {
		errs = append(errs, fmt.Errorf("emit event: %w", err))
	}
	if err := shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("shutdown: %w", err))
	}
	return errors.Join(errs...)
}

func main() {
	sink, err := httpbatch.New(httpbatch.Config{
		URL:           "http://127.0.0.1:9308/ingest",
		FlushInterval: 250 * time.Millisecond,
	})
	if err != nil {
		log.Fatal(err)
	}

	if err := loza.Configure(
		loza.Production().
			WithService("checkout").
			WithSink(sink),
	); err != nil {
		log.Fatal(err)
	}

	ctx := loza.StartEvent(context.Background(), loza.Params{
		Event:  "checkout.request",
		Method: "POST",
		Path:   "/checkout",
		Route:  "/checkout",
	})

	loza.Enrich(ctx, loza.String("payment.provider", "stripe"))
	loza.Finish(ctx, "success", loza.Int("status_code", 200))

	time.Sleep(500 * time.Millisecond)

	if err := deliverAndShutdown(ctx, loza.Emit, loza.Shutdown); err != nil {
		log.Print(err)
	}
}
