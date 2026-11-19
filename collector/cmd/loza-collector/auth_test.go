package main

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mtlsTestRequest(certs ...*x509.Certificate) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/ingest", nil)
	req.TLS = &tls.ConnectionState{PeerCertificates: certs}
	return req
}

func mtlsTestCert(cn string, dnsNames, emails []string) *x509.Certificate {
	return &x509.Certificate{
		Subject:        pkix.Name{CommonName: cn},
		DNSNames:       dnsNames,
		EmailAddresses: emails,
	}
}

func mtlsTestState(cns, dns, emails []string) *collectorState {
	return &collectorState{cfg: collectorConfig{
		authEnabled:       true,
		identityMode:      "mtls",
		mtlsAllowedCNs:    cns,
		mtlsAllowedDNS:    dns,
		mtlsAllowedEmails: emails,
	}}
}

// TestAuthorizeMTLSFailsClosedWithoutAllowlist reproduces the reported defect:
// with no allowlists configured, a client certificate with a non-empty CN must
// NOT be granted admin authorization. The previous "backward compat" branch
// returned true for any such certificate.
func TestAuthorizeMTLSFailsClosedWithoutAllowlist(t *testing.T) {
	state := mtlsTestState(nil, nil, nil)
	req := mtlsTestRequest(mtlsTestCert("any-client", nil, nil))

	if state.authorizeMTLS(req) {
		t.Fatal("authorizeMTLS granted access to a certificate with a non-empty CN when no allowlist was configured")
	}
	if state.isAuthorized(req) {
		t.Fatal("isAuthorized granted admin access with no mTLS identity allowlist configured")
	}
}

func TestAuthorizeMTLSRejectsNilAndEmptyCNWithoutAllowlist(t *testing.T) {
	state := mtlsTestState(nil, nil, nil)

	cases := []struct {
		name  string
		certs []*x509.Certificate
	}{
		{name: "no peer certificate"},
		{name: "nil certificate", certs: []*x509.Certificate{nil}},
		{name: "empty CN", certs: []*x509.Certificate{mtlsTestCert("", nil, nil)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if state.authorizeMTLS(mtlsTestRequest(tc.certs...)) {
				t.Fatal("authorizeMTLS unexpectedly granted access")
			}
		})
	}
}

func TestAuthorizeMTLSCNAllowlist(t *testing.T) {
	state := mtlsTestState([]string{"trusted-client"}, nil, nil)

	if !state.authorizeMTLS(mtlsTestRequest(mtlsTestCert("trusted-client", nil, nil))) {
		t.Fatal("matching CN should be authorized")
	}
	if state.authorizeMTLS(mtlsTestRequest(mtlsTestCert("untrusted-client", nil, nil))) {
		t.Fatal("non-matching CN must not be authorized")
	}
}

func TestAuthorizeMTLSDNSAndEmailAllowlists(t *testing.T) {
	dnsState := mtlsTestState(nil, []string{"agent.example.test"}, nil)
	if !dnsState.authorizeMTLS(mtlsTestRequest(mtlsTestCert("client", []string{"agent.example.test"}, nil))) {
		t.Fatal("matching DNS name should be authorized")
	}
	if dnsState.authorizeMTLS(mtlsTestRequest(mtlsTestCert("client", []string{"other.example.test"}, nil))) {
		t.Fatal("non-matching DNS name must not be authorized")
	}

	emailState := mtlsTestState(nil, nil, []string{"agent@example.test"})
	if !emailState.authorizeMTLS(mtlsTestRequest(mtlsTestCert("client", nil, []string{"agent@example.test"}))) {
		t.Fatal("matching email should be authorized")
	}
	if emailState.authorizeMTLS(mtlsTestRequest(mtlsTestCert("client", nil, []string{"other@example.test"}))) {
		t.Fatal("non-matching email must not be authorized")
	}
}

func TestValidateFileConfigRejectsMTLSWithoutAllowlist(t *testing.T) {
	cfg := validFileConfig()
	cfg.Identity.Mode = "mtls"
	if err := validateFileConfig(cfg); err == nil || !strings.Contains(err.Error(), "mtls_allowed") {
		t.Fatalf("expected mTLS-without-allowlist rejection, got %v", err)
	}

	cfg.Identity.MTLSAllowedCNs = []string{"trusted-client"}
	if err := validateFileConfig(cfg); err != nil {
		t.Fatalf("mTLS with a CN allowlist should be valid: %v", err)
	}
}
