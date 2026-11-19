package main

import (
	"strings"
	"testing"
)

func TestParseTrustedCIDRs(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got, err := parseTrustedCIDRs(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected no CIDRs, got %d", len(got))
		}
	})

	t.Run("valid", func(t *testing.T) {
		got, err := parseTrustedCIDRs([]string{"10.0.0.0/8", "2001:db8::/32"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 CIDRs, got %d", len(got))
		}
	})

	t.Run("invalid entry reports offending CIDR", func(t *testing.T) {
		_, err := parseTrustedCIDRs([]string{"10.0.0.0/8", "not-a-cidr"})
		if err == nil {
			t.Fatal("expected an error for invalid CIDR")
		}
		if !strings.Contains(err.Error(), "not-a-cidr") || !strings.Contains(err.Error(), "invalid trusted_proxies CIDR") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})
}

// Regression for MASS-0125: an invalid trusted_proxies CIDR used to trigger
// log.Fatal (os.Exit) after deferred resource cleanup had been registered, so
// the process exited without running any defers. The collector must instead
// return an error through normal control flow.
func TestRunCollectorInvalidTrustedProxyCIDRReturnsError(t *testing.T) {
	cfg := collectorConfig{trustedProxies: []string{"definitely-not-a-cidr"}}

	err := runCollector(cfg)
	if err == nil {
		t.Fatal("expected runCollector to return an error for an invalid CIDR")
	}
	if !strings.Contains(err.Error(), "invalid trusted_proxies CIDR") || !strings.Contains(err.Error(), "definitely-not-a-cidr") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateFileConfigRejectsInvalidTrustedProxyCIDR(t *testing.T) {
	cfg := validFileConfig()
	cfg.Identity.TrustedProxies = []string{"203.0.113.0/24", "bad-cidr"}

	err := validateFileConfig(cfg)
	if err == nil {
		t.Fatal("expected config validation to reject invalid trusted proxy CIDR")
	}
	if !strings.Contains(err.Error(), "invalid trusted_proxies CIDR") || !strings.Contains(err.Error(), "bad-cidr") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateFileConfigAcceptsValidTrustedProxyCIDR(t *testing.T) {
	cfg := validFileConfig()
	cfg.Identity.TrustedProxies = []string{"203.0.113.0/24", "2001:db8::/32"}

	if err := validateFileConfig(cfg); err != nil {
		t.Fatalf("valid trusted proxy CIDRs rejected: %v", err)
	}
}
