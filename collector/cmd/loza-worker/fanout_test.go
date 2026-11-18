package main

import (
	"strings"
	"testing"
)

func TestValidateFanoutRolesRejectsMultipleEnabledFallbacks(t *testing.T) {
	outputs := []workerFanoutOutput{
		{name: "fb-a", role: fanoutRoleFallback, enabled: true},
		{name: "fb-b", role: fanoutRoleFallback, enabled: true},
	}
	err := validateFanoutRoles(outputs)
	if err == nil {
		t.Fatal("expected an error for multiple enabled fallback outputs")
	}
	if !strings.Contains(err.Error(), "at most one") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateFanoutRolesAllowsSingleEnabledFallback(t *testing.T) {
	outputs := []workerFanoutOutput{
		{name: "fb-a", role: fanoutRoleFallback, enabled: true},
		{name: "fb-disabled", role: fanoutRoleFallback, enabled: false},
		{name: "sec", role: fanoutRoleSecondary, enabled: true},
	}
	if err := validateFanoutRoles(outputs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Regression for MASS-0143: two fallback outputs must be rejected before any
// sink is created, so the earlier fallback cannot be silently overwritten and
// leak its background flush loop/resources.
func TestCreateFanoutSinksRejectsMultipleFallbacks(t *testing.T) {
	cfg := workerConfig{
		fanoutOutputs: []workerFanoutOutput{
			{name: "fb-a", role: fanoutRoleFallback, enabled: true},
			{name: "fb-b", role: fanoutRoleFallback, enabled: true},
		},
	}
	secondary, fallback, dbs, err := createFanoutSinks(cfg)
	if err == nil {
		t.Fatal("expected createFanoutSinks to reject multiple fallback outputs")
	}
	if !strings.Contains(err.Error(), "at most one") {
		t.Fatalf("unexpected error: %v", err)
	}
	if secondary != nil || fallback != nil || dbs != nil {
		t.Fatalf("expected no sinks on validation failure, got secondary=%v fallback=%v dbs=%v", secondary, fallback, dbs)
	}
}
