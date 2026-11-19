package main

import (
	"context"
	"fmt"

	loza "github.com/astraive/loza/sdks/go"
)

// runAuditExample creates the "audit-service" alias and logs the audit trail.
// It checks the error returned by the alias factory before using the logger,
// returning a descriptive error instead of dereferencing a nil logger when
// aliasing fails. A nil factory uses loza.Alias.
func runAuditExample(makeAlias func(string) (*loza.Logger, error)) error {
	if makeAlias == nil {
		makeAlias = loza.Alias
	}
	audit, err := makeAlias("audit-service")
	if err != nil {
		return fmt.Errorf("create audit alias: %w", err)
	}
	audit.Info("audit trail started")
	return nil
}

func main() {
	ctx := context.Background()

	// Default API — configure once, use everywhere
	if err := loza.Configure(loza.ApplyConfig(loza.Production(),
		loza.WithService("quickstart-demo"),
		loza.WithCollectorEndpoint("http://localhost:9308"),
	)); err != nil {
		fmt.Printf("configure error: %v\n", err)
		fmt.Printf("configure default logger: %v\n", err)
		return
	}
	defer loza.Shutdown(ctx)

	loza.Info("server started")

	ev := loza.StartEvent(ctx, loza.Params{Event: "user.signup", Kind: "http"})
	loza.Enrich(ev, loza.String("user.email", "demo@example.com"))
	loza.Enrich(ev, loza.String("user.plan", "pro"))
	loza.Finish(ev, "success")
	if err := loza.Emit(ev); err != nil {
		fmt.Printf("emit error: %v\n", err)
		return
	}
	fmt.Println("Event emitted successfully")

	// Custom instance
	logger, err := loza.New(loza.ApplyConfig(loza.Config{},
		loza.WithService("checkout-api"),
		loza.WithCollectorEndpoint("http://localhost:9308"),
	))
	if err != nil {
		fmt.Printf("logger creation error: %v\n", err)
		return
	}
	logger.Info("custom instance ready")

	// Alias — same config, different service name
	if err := runAuditExample(loza.Alias); err != nil {
		fmt.Printf("audit alias error: %v\n", err)
		return
	}
	if err := newCustomLogger(
		loza.WithService("checkout-api"),
		loza.WithCollectorEndpoint("http://localhost:9308"),
	); err != nil {
		fmt.Printf("%v\n", err)
		return
	}

	// Alias — same config, different service name
	audit, err := loza.Alias("audit-service")
	if err != nil {
		fmt.Printf("create audit alias: %v\n", err)
		return
	}
	audit.Info("audit trail started")
}

// newCustomLogger builds a custom logger from options and logs a readiness
// message. A configuration validation error is returned to the caller instead
// of leaving a nil logger to be dereferenced by Info.
func newCustomLogger(options ...loza.ConfigOption) error {
	logger, err := loza.New(loza.ApplyConfig(loza.Config{}, options...))
	if err != nil {
		return fmt.Errorf("create custom logger: %w", err)
	}
	logger.Info("custom instance ready")
	return nil
}
