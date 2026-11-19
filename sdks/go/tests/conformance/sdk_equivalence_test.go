package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/astraive/loza/sdks/go"
)

// canonicalFields are the fields the canonical fixture and the SDK-emitted
// event must agree on for this equivalence test to be meaningful.
var canonicalFields = []string{"schema_version", "event_version", "service", "event", "kind", "level", "outcome"}

// eventFixtureDivergences returns a description for every canonical field whose
// value in got differs from the value in fixture. Keeping this logic in a pure
// helper lets it be tested directly (see TestEventFixtureDivergencesDetectsDivergence)
// so the equivalence assertions cannot silently become a no-op again.
func eventFixtureDivergences(got, fixture map[string]any) []string {
	var divergences []string
	for _, field := range canonicalFields {
		want, wantOK := fixture[field]
		have, haveOK := got[field]
		if !wantOK || !haveOK || have != want {
			divergences = append(divergences, fmt.Sprintf("%s: fixture=%v (present=%t), SDK=%v (present=%t)", field, want, wantOK, have, haveOK))
		}
	}
	return divergences
}

func TestSDKOutputEquivalence(t *testing.T) {
	// Load canonical fixture
	fixturePath := filepath.Join("..", "..", "..", "..", "spec", "fixtures", "sdk-equivalence", "canonical_event.json")
	fixtureData, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture not found: %v", err)
	}

	var fixture map[string]any
	if err := json.Unmarshal(fixtureData, &fixture); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	// Create event using SDK
	sink, store := loza.MemorySink()
	cfg := loza.Test().
		WithService("checkout").
		WithSink(sink).
		WithSampler(loza.SampleAll()).
		WithAsync(false)
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}

	ctx := loza.StartEvent(context.Background(), loza.Params{
		Service: "checkout",
		Event:   "payment.completed",
		Kind:    "event",
		Level:   loza.LevelInfo,
	})
	_ = loza.Enrich(ctx,
		loza.Float64("amount", 99.99),
		loza.String("currency", "USD"),
		loza.String("user.id", "u_123"),
	)
	_ = loza.Finish(ctx, "success")
	_ = loza.Emit(ctx)
	_ = loza.Flush(context.Background())

	if store.Len() == 0 {
		t.Fatal("no events emitted")
	}

	// Decode the SDK-emitted event and compare it to the canonical fixture.
	raw := store.Raw()
	if len(raw) == 0 || len(raw[0]) == 0 {
		t.Fatal("no raw event emitted")
	}
	var emitted map[string]any
	if err := json.Unmarshal(raw[0], &emitted); err != nil {
		t.Fatalf("unmarshal SDK-emitted event: %v", err)
	}
	for _, divergence := range eventFixtureDivergences(emitted, fixture) {
		t.Errorf("SDK output diverges from canonical fixture: %s", divergence)
	}

	// Verify the fixture itself has the expected fields
	for _, field := range canonicalFields {
		if _, ok := fixture[field]; !ok {
			t.Errorf("fixture missing expected field: %s", field)
		}
	}

	// Verify fixture values
	if fixture["schema_version"] != "v1" {
		t.Errorf("fixture schema_version: expected v1, got %v", fixture["schema_version"])
	}
	if fixture["event_version"] != "v1" {
		t.Errorf("fixture event_version: expected v1, got %v", fixture["event_version"])
	}
	if fixture["service"] != "checkout" {
		t.Errorf("fixture service: expected checkout, got %v", fixture["service"])
	}
	if fixture["event"] != "payment.completed" {
		t.Errorf("fixture event: expected payment.completed, got %v", fixture["event"])
	}
	if fixture["kind"] != "event" {
		t.Errorf("fixture kind: expected event, got %v", fixture["kind"])
	}
	if fixture["level"] != "info" {
		t.Errorf("fixture level: expected info, got %v", fixture["level"])
	}
	if fixture["outcome"] != "success" {
		t.Errorf("fixture outcome: expected success, got %v", fixture["outcome"])
	}
}

// TestEventFixtureDivergencesDetectsDivergence is a regression test for the
// finding that the "equivalence" test never compared SDK output to the fixture
// and therefore passed even when the emitted event held wrong values. It proves
// the comparison applied in TestSDKOutputEquivalence actually reports mismatches.
func TestEventFixtureDivergencesDetectsDivergence(t *testing.T) {
	fixture := map[string]any{
		"schema_version": "v1",
		"event_version":  "v1",
		"service":        "checkout",
		"event":          "payment.completed",
		"kind":           "event",
		"level":          "info",
		"outcome":        "success",
	}

	if got := eventFixtureDivergences(fixture, fixture); len(got) != 0 {
		t.Fatalf("expected no divergences for identical maps, got %v", got)
	}

	// A wrong value and a missing field must both be reported.
	mutated := make(map[string]any, len(fixture))
	for k, v := range fixture {
		mutated[k] = v
	}
	mutated["service"] = "wrong-service"
	delete(mutated, "outcome")

	divergences := eventFixtureDivergences(mutated, fixture)
	if len(divergences) != 2 {
		t.Fatalf("expected 2 divergences (service, outcome), got %d: %v", len(divergences), divergences)
	}
	report := strings.Join(divergences, " ")
	for _, want := range []string{"service", "outcome"} {
		if !strings.Contains(report, want) {
			t.Errorf("expected divergence report to mention %q, got %q", want, report)
		}
	}
}
