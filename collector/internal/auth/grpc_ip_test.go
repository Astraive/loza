package auth

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

// TestExtractIPFromPeer_XRealIPValidatedAndCanonicalized is a regression test
// for the same X-Real-IP allowlist bypass in the gRPC path: the x-real-ip
// metadata value must be validated and canonicalized, never returned verbatim.
func TestExtractIPFromPeer_XRealIPValidatedAndCanonicalized(t *testing.T) {
	trusted := []*net.IPNet{mustParseCIDR(t, "10.0.0.0/8")}

	newCtx := func(peerIP, xri string) context.Context {
		ctx := peer.NewContext(context.Background(), &peer.Peer{
			Addr: &net.TCPAddr{IP: net.ParseIP(peerIP), Port: 1234},
		})
		md := metadata.Pairs("x-real-ip", xri)
		return metadata.NewIncomingContext(ctx, md)
	}

	tests := []struct {
		name   string
		peerIP string
		xri    string
		want   string
	}{
		{"valid x-real-ip is returned", "10.0.0.1", "203.0.113.5", "203.0.113.5"},
		{"valid x-real-ip is canonicalized", "10.0.0.1", "2001:0db8:0000::1", "2001:db8::1"},
		{"malformed x-real-ip falls back to peer", "10.0.0.1", "not-an-ip", "10.0.0.1"},
		{"x-real-ip ignored for untrusted peer", "192.0.2.10", "203.0.113.5", "192.0.2.10"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractIPFromPeer(newCtx(tt.peerIP, tt.xri), trusted); got != tt.want {
				t.Errorf("extractIPFromPeer() = %q, want %q", got, tt.want)
			}
		})
	}
}
