package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/astraive/loza/sdks/go"
)

// errorOutput is where errors returned by mutating SDK calls are reported. It
// is a variable so regression tests can capture the output; production code
// writes to stdout.
var errorOutput io.Writer = os.Stdout

// reportError annotates and logs an error returned by a mutating SDK call so
// the demo never silently discards a failed operation. It returns the wrapped
// error, allowing callers to propagate it up the lifecycle.
func reportError(operation string, err error) error {
	if err == nil {
		return nil
	}
	wrapped := fmt.Errorf("%s: %w", operation, err)
	fmt.Fprintf(errorOutput, "   %v\n", wrapped)
	return wrapped
}

// runExample runs one demo section and reports any error it propagates so the
// example itself does not discard the result of a mutating call.
func runExample(name string, fn func() error) {
	fmt.Println(name)
	if err := fn(); err != nil {
		fmt.Printf("   Error: %v\n", err)
	}
	fmt.Println()
}

func main() {
	// Configure the SDK
	cfg := loza.Dev()
	if err := loza.Configure(cfg); err != nil {
		panic(err)
	}
	defer loza.Shutdown(context.Background())

	fmt.Println("=== LOZA Event State Machine Demo ===")

	// Example 1: Happy path - complete lifecycle
	runExample("Example 1: Complete Event Lifecycle", happyPath)

	// Example 2: Error handling
	runExample("Example 2: Error Handling", errorHandling)

	// Example 3: Invalid state transitions
	runExample("Example 3: Invalid State Transitions", invalidTransitions)

	// Example 4: Array field operations with Add
	runExample("Example 4: Array Field Operations", arrayOperations)
	fmt.Println("Example 4: Array Field Operations")
	arrayOperations()
	fmt.Println()

	// Example 5: State observability / progression
	fmt.Println("Example 5: State Observability")
	demonstrateStateObservability()
	fmt.Println()
}

func happyPath() error {
	ctx := loza.StartEvent(context.Background(), loza.Params{
		Event: "order.process",
	})
	return happyPathWithContext(ctx)
}

// happyPathWithContext contains the lifecycle logic so tests can supply a
// context (for example, one holding a closed event) and observe propagation.
func happyPathWithContext(ctx context.Context) error {
	ev, _ := loza.FromContext(ctx)
	fmt.Printf("1. Initial state: %s\n", ev.State())

	// Enrich transitions to active
	if err := loza.Enrich(ctx,
		loza.String("order_id", "ORD-123"),
		loza.String("customer_id", "CUST-456"),
	); err != nil {
		return reportError("Enrich", err)
	}
	fmt.Printf("2. After Enrich: %s\n", ev.State())

	// Set also keeps it active
	if err := loza.Set(ctx,
		loza.Float64("total", 99.99),
		loza.String("currency", "USD"),
	); err != nil {
		return reportError("Set", err)
	}
	fmt.Printf("3. After Set: %s\n", ev.State())

	// Finish transitions to finished
	if err := loza.Finish(ctx, "success",
		loza.Int("items_processed", 3),
	); err != nil {
		return reportError("Finish", err)
	}
	fmt.Printf("4. After Finish: %s\n", ev.State())

	// Emit transitions to emitted
	if err := loza.Emit(ctx); err != nil {
		return reportError("Emit", err)
	}
	fmt.Printf("5. After Emit: %s\n", ev.State())
	return nil
}

func errorHandling() error {
	ctx := loza.StartEvent(context.Background(), loza.Params{
		Event: "payment.process",
	})

	ev, _ := loza.FromContext(ctx)
	fmt.Printf("1. Initial state: %s\n", ev.State())

	if err := loza.Enrich(ctx,
		loza.String("payment_id", "PAY-789"),
		loza.Float64("amount", 49.99),
	); err != nil {
		return reportError("Enrich", err)
	}
	fmt.Printf("2. After Enrich: %s\n", ev.State())

	// Simulate an error
	paymentErr := errors.New("insufficient funds")
	if err := loza.FinishError(ctx, paymentErr,
		loza.Bool("retryable", true),
		loza.String("error_code", "INSUFFICIENT_FUNDS"),
	); err != nil {
		return reportError("FinishError", err)
	}
	fmt.Printf("3. After FinishError: %s\n", ev.State())

	// Emit the error event
	if err := loza.Emit(ctx); err != nil {
		return reportError("Emit", err)
	}
	fmt.Printf("4. After Emit: %s\n", ev.State())
	return nil
}

func invalidTransitions() error {
	ctx := loza.StartEvent(context.Background(), loza.Params{
		Event: "test.invalid",
	})

	ev, _ := loza.FromContext(ctx)

	// Complete the lifecycle
	if err := loza.Enrich(ctx, loza.String("key", "value")); err != nil {
		return reportError("Enrich", err)
	}
	if err := loza.Finish(ctx, "success"); err != nil {
		return reportError("Finish", err)
	}
	if err := loza.Emit(ctx); err != nil {
		return reportError("Emit", err)
	}

	fmt.Printf("Event state: %s\n", ev.State())

	// Try to modify after emit
	fmt.Println("\nAttempting to modify emitted event:")
	if err := loza.Enrich(ctx, loza.String("new_key", "new_value")); err != nil {
		var closed *loza.EventClosedError
		if errors.As(err, &closed) {
			fmt.Printf("✗ Cannot modify: Event %s is in state %s\n", closed.EventID, closed.State)
		} else {
			return reportError("Enrich", err)
		}
	}

	// Try to finish after emit
	fmt.Println("\nAttempting to finish emitted event:")
	if err := loza.Finish(ctx, "success"); err != nil {
		var closed *loza.EventClosedError
		if errors.As(err, &closed) {
			fmt.Printf("✗ Cannot finish: Event %s is in state %s\n", closed.EventID, closed.State)
		} else {
			return reportError("Finish", err)
		}
	}

	// Try to emit again
	fmt.Println("\nAttempting to emit again:")
	if err := loza.Emit(ctx); err != nil {
		var duplicate *loza.DuplicateEmitError
		if errors.As(err, &duplicate) {
			fmt.Printf("✗ Cannot emit: Event %s already emitted\n", duplicate.EventID)
		} else {
			return reportError("Emit", err)
		}
	}
	return nil
}

func arrayOperations() error {
	ctx := loza.StartEvent(context.Background(), loza.Params{
		Event: "shopping.cart",
	})

	ev, _ := loza.FromContext(ctx)
	fmt.Printf("Initial state: %s\n", ev.State())

	// Add items to cart using Add method
	fmt.Println("\nAdding items to cart:")
	if err := loza.Add(ctx, "items", "ITEM-001"); err != nil {
		return reportError("Add ITEM-001", err)
	}
	fmt.Println("  Added ITEM-001")

	if err := loza.Add(ctx, "items", "ITEM-002"); err != nil {
		return reportError("Add ITEM-002", err)
	}
	fmt.Println("  Added ITEM-002")

	if err := loza.Add(ctx, "items", "ITEM-003"); err != nil {
		return reportError("Add ITEM-003", err)
	}
	fmt.Println("  Added ITEM-003")

	// Retrieve the items
	if items, ok := ev.Get("items"); ok {
		fmt.Printf("\nCart items: %v\n", items)
	}

	// Add tags
	fmt.Println("\nAdding tags:")
	if err := loza.Add(ctx, "tags", "priority"); err != nil {
		return reportError("Add priority", err)
	}
	if err := loza.Add(ctx, "tags", "express-shipping"); err != nil {
		return reportError("Add express-shipping", err)
	}

	if tags, ok := ev.Get("tags"); ok {
		fmt.Printf("Tags: %v\n", tags)
	}

	fmt.Printf("\nState after Add operations: %s\n", ev.State())

	// Complete the event
	if err := loza.Finish(ctx, "success"); err != nil {
		return reportError("Finish", err)
	}
	if err := loza.Emit(ctx); err != nil {
		return reportError("Emit", err)
	}
	fmt.Printf("Final state: %s\n", ev.State())
	return nil
}

func demonstrateStateObservability() error {
	ctx := loza.StartEvent(context.Background(), loza.Params{
		Event: "state.tracking",
	})

	ev, _ := loza.FromContext(ctx)

	states := []string{}
	states = append(states, string(ev.State()))

	if err := loza.Enrich(ctx, loza.String("step", "1")); err != nil {
		return reportError("Enrich", err)
	}
	states = append(states, string(ev.State()))

	if err := loza.Set(ctx, loza.String("step", "2")); err != nil {
		return reportError("Set", err)
	}
	states = append(states, string(ev.State()))

	if err := loza.Finish(ctx, "success"); err != nil {
		return reportError("Finish", err)
	}
	states = append(states, string(ev.State()))

	if err := loza.Emit(ctx); err != nil {
		return reportError("Emit", err)
	}
	states = append(states, string(ev.State()))

	fmt.Println("State progression:")
	for i, state := range states {
		fmt.Printf("  %d. %s\n", i+1, state)
	}
	return nil
}
