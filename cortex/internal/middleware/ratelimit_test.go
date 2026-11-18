package middleware

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, cidr, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", s, err)
	}
	return cidr
}

func TestExtractClientIPUsesRightmostUntrustedForwardedHop(t *testing.T) {
	trusted := []*net.IPNet{mustCIDR(t, "10.0.0.0/8")}
	rl := NewRateLimiter(100, 100, WithTrustedProxies(trusted))

	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		realIP     string
		want       string
	}{
		{
			name:       "spoofed leftmost entry is ignored",
			remoteAddr: "10.0.0.5:1234",
			xff:        "1.2.3.4, 198.51.100.20",
			want:       "198.51.100.20",
		},
		{
			name:       "rightmost trusted proxy hop is skipped",
			remoteAddr: "10.0.0.5:1234",
			xff:        "1.2.3.4, 198.51.100.20, 10.1.2.3",
			want:       "198.51.100.20",
		},
		{
			name:       "invalid rightmost entry is skipped",
			remoteAddr: "10.0.0.5:1234",
			xff:        "198.51.100.20, not-an-ip",
			want:       "198.51.100.20",
		},
		{
			name:       "real client ip header is canonicalized",
			remoteAddr: "10.0.0.5:1234",
			xff:        "1.2.3.4, 2001:0db8::1",
			want:       "2001:db8::1",
		},
		{
			name:       "only trusted hops falls back to x-real-ip",
			remoteAddr: "10.0.0.5:1234",
			xff:        "10.1.1.1, 10.2.2.2",
			realIP:     " 203.0.113.7 ",
			want:       "203.0.113.7",
		},
		{
			name:       "only trusted hops falls back to remote address",
			remoteAddr: "10.0.0.5:1234",
			xff:        "10.1.1.1, 10.2.2.2",
			want:       "10.0.0.5",
		},
		{
			name:       "xff ignored from untrusted peer",
			remoteAddr: "203.0.113.9:5555",
			xff:        "1.2.3.4, 198.51.100.20",
			want:       "203.0.113.9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.realIP != "" {
				req.Header.Set("X-Real-IP", tt.realIP)
			}
			if got := rl.extractClientIP(req); got != tt.want {
				t.Fatalf("extractClientIP() = %q, want %q", got, tt.want)
			}
		})
	}

	noProxies := NewRateLimiter(100, 100)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := noProxies.extractClientIP(req); got != "10.0.0.5" {
		t.Fatalf("xff must be ignored when no trusted proxies are configured, got %q", got)
	}
}

func TestRateLimiterMiddlewareIgnoresSpoofedXFFForPerIPLimit(t *testing.T) {
	trusted := []*net.IPNet{mustCIDR(t, "10.0.0.0/8")}
	rl := NewRateLimiter(60, 60, WithTrustedProxies(trusted))
	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Every request comes from the same real client as observed by the trusted
	// proxy (the rightmost XFF hop), but the attacker rotates a fake leftmost
	// entry to try to obtain a fresh per-IP bucket each time.
	const requests = 20
	for i := 0; i < requests; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.5:1234"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d, 198.51.100.20", i+1))
		handler.ServeHTTP(rec, req)
	}

	rl.mu.Lock()
	buckets := len(rl.ips)
	_, hasRealClient := rl.ips["198.51.100.20"]
	rl.mu.Unlock()
	if buckets != 1 || !hasRealClient {
		t.Fatalf("expected one per-IP bucket keyed by 198.51.100.20, got %d buckets (real client present: %t)", buckets, hasRealClient)
	}

	// The shared bucket is now exhausted: a further request from the same real
	// client is rejected even when it carries another spoofed leftmost entry.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("X-Forwarded-For", "192.0.2.99, 198.51.100.20")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected rate limit rejection for shared client bucket, got %d", rec.Code)
	}
}
