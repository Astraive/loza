package middleware

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mustParseTrustedCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, cidr, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", s, err)
	}
	return cidr
}

// TestExtractClientIP_XRealIPValidatedAndCanonicalized is a regression test for
// the X-Real-IP allowlist bypass: the header must be validated with net.ParseIP
// and canonicalized, never returned verbatim. Malformed input falls back to the
// trusted proxy's own address.
func TestExtractClientIP_XRealIPValidatedAndCanonicalized(t *testing.T) {
	rl := &RateLimiter{trustedProxies: []*net.IPNet{mustParseTrustedCIDR(t, "10.0.0.0/8")}}

	tests := []struct {
		name       string
		remoteAddr string
		xri        string
		want       string
	}{
		{"valid X-Real-IP is returned", "10.0.0.1:1234", "203.0.113.5", "203.0.113.5"},
		{"valid X-Real-IP is canonicalized", "10.0.0.1:1234", "2001:0db8::0001", "2001:db8::1"},
		{"malformed X-Real-IP falls back to proxy", "10.0.0.1:1234", "not-an-ip", "10.0.0.1"},
		{"X-Real-IP ignored for untrusted peer", "192.0.2.10:1234", "203.0.113.5", "192.0.2.10"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			r.Header.Set("X-Real-IP", tt.xri)

			if got := rl.extractClientIP(r); got != tt.want {
				t.Errorf("extractClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
