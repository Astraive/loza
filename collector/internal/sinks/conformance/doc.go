// Package conformance contains reusable sink behavior checks.
//
// Every production sink should wire this suite into its package tests. The
// suite intentionally starts with the collector's current event.Sink interface
// and can be extended as the full health, batch, idempotency, timeout,
// migration, and partial-failure sink contract lands.
//
// There is deliberately no RequiresIdempotent option: event.Sink is a write-only
// contract with no way to observe delivered events, so an idempotency check
// could not assert "no duplicate effect" and would only be a false promise.
// Such an option should be reintroduced only together with an observation hook
// that lets the suite count deliveries per event ID.
package conformance
