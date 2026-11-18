package auth

import (
	"context"
	"net"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

func trustedProxiesForTest(t *testing.T, cidrs ...string) []*net.IPNet {
	t.Helper()
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatalf("net.ParseCIDR(%q): %v", cidr, err)
		}
		nets = append(nets, n)
	}
	return nets
}

func TestExtractIPRejectsInvalidXRealIP(t *testing.T) {
	trusted := trustedProxiesForTest(t, "10.0.0.0/8")

	for _, xri := range []string{
		"attacker-controlled-key",
		"not-an-ip",
		"203.0.113.10:8080",
	} {
		t.Run(xri, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://example.com/", nil)
			r.RemoteAddr = "10.0.0.1:12345"
			r.Header.Set("X-Real-IP", xri)

			if got := extractIP(r, trusted); got != "10.0.0.1" {
				t.Fatalf("extractIP() = %q, want %q", got, "10.0.0.1")
			}
		})
	}
}

func TestExtractIPAcceptsValidXRealIP(t *testing.T) {
	trusted := trustedProxiesForTest(t, "10.0.0.0/8")

	r := httptest.NewRequest("GET", "http://example.com/", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	r.Header.Set("X-Real-IP", " 203.0.113.7 ")

	if got := extractIP(r, trusted); got != "203.0.113.7" {
		t.Fatalf("extractIP() = %q, want %q", got, "203.0.113.7")
	}
}

func TestExtractIPFromPeerRejectsInvalidXRealIP(t *testing.T) {
	trusted := trustedProxiesForTest(t, "10.0.0.0/8")

	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 12345},
	})
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("x-real-ip", "attacker-controlled-key"))

	if got := extractIPFromPeer(ctx, trusted); got != "10.0.0.1" {
		t.Fatalf("extractIPFromPeer() = %q, want %q", got, "10.0.0.1")
	}
}

func TestExtractIPFromPeerAcceptsValidXRealIP(t *testing.T) {
	trusted := trustedProxiesForTest(t, "10.0.0.0/8")

	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 12345},
	})
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("x-real-ip", "203.0.113.7"))

	if got := extractIPFromPeer(ctx, trusted); got != "203.0.113.7" {
		t.Fatalf("extractIPFromPeer() = %q, want %q", got, "203.0.113.7")
	}
}
