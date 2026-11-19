package auth

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, cidr, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", s, err)
	}
	return cidr
}

// TestExtractIP_PrefersRightmostUntrustedXFF reproduces the spoofing reported
// in MASS-0157: a client prepends an allowed IP to X-Forwarded-For, the trusted
// proxy appends the real client IP, and selection must use the appended (right)
// value rather than the client-controlled (left) value.
func TestExtractIP_PrefersRightmostUntrustedXFF(t *testing.T) {
	trusted := []*net.IPNet{testCIDR(t, "10.0.0.0/8")}

	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		realIP     string
	}{
		{
			name:       "spoofed allowed IP prepended is ignored",
			remoteAddr: "10.0.0.1:1234",
			xff:        "203.0.113.10, 198.51.100.20",
			realIP:     "198.51.100.20",
		},
		{
			name:       "several spoofed entries prepended are ignored",
			remoteAddr: "10.0.0.1:1234",
			xff:        "203.0.113.10, 203.0.113.11, 198.51.100.20",
			realIP:     "198.51.100.20",
		},
		{
			name:       "trusted proxy hop on the right is skipped",
			remoteAddr: "10.0.0.2:1234",
			xff:        "198.51.100.20, 10.0.0.5",
			realIP:     "198.51.100.20",
		},
		{
			name:       "invalid rightmost entry is skipped",
			remoteAddr: "10.0.0.1:1234",
			xff:        "203.0.113.10, not-an-ip",
			realIP:     "203.0.113.10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/events", nil)
			req.RemoteAddr = tt.remoteAddr
			req.Header.Set("X-Forwarded-For", tt.xff)
			if got := extractIP(req, trusted); got != tt.realIP {
				t.Fatalf("extractIP() = %q, want rightmost untrusted IP %q (XFF %q)", got, tt.realIP, tt.xff)
			}
		})
	}
}

func TestExtractIP_XFFIgnoredFromUntrustedPeer(t *testing.T) {
	trusted := []*net.IPNet{testCIDR(t, "10.0.0.0/8")}
	req := httptest.NewRequest(http.MethodPost, "/events", nil)
	req.RemoteAddr = "198.51.100.20:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.10")

	if got := extractIP(req, trusted); got != "198.51.100.20" {
		t.Fatalf("extractIP() = %q, want direct peer 198.51.100.20", got)
	}
}

type ipOnlyStore struct {
	record *KeyRecord
}

func (s *ipOnlyStore) FindByKeyID(_ context.Context, _ string) (*KeyRecord, error) {
	return s.record, nil
}

// TestMiddleware_AllowedIPsRejectsSpoofedXFF verifies the security impact: a key
// restricted to the attacker's spoofed address must not be satisfied when the
// real client IP (appended by the trusted proxy) differs from that address.
func TestMiddleware_AllowedIPsRejectsSpoofedXFF(t *testing.T) {
	serverSecret := []byte("test-server-secret")
	store := &ipOnlyStore{record: &KeyRecord{
		ID:         "id_ip1",
		KeyID:      "kip1",
		Kind:       KeyKindSecret,
		Roles:      []Role{RoleIngestServer},
		SecretHash: HashSecret("ipsecret", serverSecret),
		AllowedIPs: []string{"203.0.113.10"},
	}}
	cache := NewMemoryKeyCache(10*time.Second, 5*time.Second)
	defer cache.Close()

	trusted := []*net.IPNet{testCIDR(t, "10.0.0.0/8")}
	handler := Middleware(store, cache, serverSecret, WithTrustedProxies(trusted))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodPost, "/events", nil)
	req.Header.Set("Authorization", "Bearer lz_sec_live_kip1_ipsecret")
	req.RemoteAddr = "10.0.0.1:1234"
	// Attacker prepends an allowed IP; trusted proxy appends the real client.
	req.Header.Set("X-Forwarded-For", "203.0.113.10, 198.51.100.20")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d: spoofed XFF must not satisfy AllowedIPs", rec.Code, http.StatusUnauthorized)
	}
}
