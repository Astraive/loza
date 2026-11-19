package models

import (
	"errors"
	"testing"
	"time"
)

func TestEventValidate(t *testing.T) {
	base := Event{
		ID:         "evt-1",
		Timestamp:  time.Now(),
		Kind:       EventKindLog,
		Service:    "svc-a",
		Provenance: "jsonl",
	}

	if err := base.Validate(); err != nil {
		t.Fatalf("expected valid event: %v", err)
	}

	tests := []struct {
		name string
		mut  func(*Event)
		want error
	}{
		{"empty id", func(e *Event) { e.ID = "" }, ErrEmptyID},
		{"future timestamp", func(e *Event) { e.Timestamp = time.Now().Add(2 * time.Minute) }, ErrInvalidTimestamp},
		{"invalid kind", func(e *Event) { e.Kind = "bad" }, ErrInvalidKind},
		{"empty service", func(e *Event) { e.Service = "" }, ErrEmptyService},
		{"invalid provenance", func(e *Event) { e.Provenance = "bad" }, ErrInvalidProvenance},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev := base
			tc.mut(&ev)
			err := ev.Validate()
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

func TestNodeAndEdgeValidate(t *testing.T) {
	node := &Node{ID: "n1", Type: NodeTypeService, Label: "svc"}
	if err := node.Validate(); err != nil {
		t.Fatalf("node should be valid: %v", err)
	}

	edge := &Edge{ID: "e1", FromNodeID: "n1", ToNodeID: "n2", Type: EdgeTypeDependsOn, Weight: 1}
	if err := edge.Validate(); err != nil {
		t.Fatalf("edge should be valid: %v", err)
	}
}

func TestOutcomeCategory(t *testing.T) {
	tests := []struct {
		code     int
		expected string
	}{
		{code: 100, expected: "pending"}, // informational: must not be "failed"
		{code: 199, expected: "pending"},
		{code: 200, expected: "success"},
		{code: 299, expected: "success"},
		{code: 300, expected: "partial"},
		{code: 399, expected: "partial"},
		{code: 400, expected: "failed"},
		{code: 500, expected: "failed"},
		{code: 599, expected: "failed"},
	}

	for _, tt := range tests {
		if got := OutcomeCategory(tt.code); got != tt.expected {
			t.Errorf("OutcomeCategory(%d) = %q, want %q", tt.code, got, tt.expected)
		}
	}
}

func TestIncidentAndFeedbackValidate(t *testing.T) {
	incident := &Incident{ID: "inc-1", Status: "active", Severity: "high"}
	if err := incident.Validate(); err != nil {
		t.Fatalf("incident should be valid: %v", err)
	}

	feedback := &RemediationFeedback{OutcomeCode: 200, OutcomeCategory: "success"}
	if err := feedback.Validate(); err != nil {
		t.Fatalf("feedback should be valid: %v", err)
	}

	if !IsValidKind(EventKindCollectorEvent) {
		t.Fatal("expected collector event kind to be valid")
	}
	if !IsValidProvenance("collector") {
		t.Fatal("expected collector provenance to be valid")
	}
	if !IsValidOutcomeCode(200) {
		t.Fatal("expected 200 to be a valid outcome code")
	}
	if !IsValidOutcomeCode(500) {
		t.Fatal("expected 500 to be a valid outcome code")
	}
}
