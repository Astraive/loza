package auth

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// grpcAuthContext builds an incoming gRPC context with a bearer credential and
// a peer whose remote address is the given IP.
func grpcAuthContext(remoteIP, token string) context.Context {
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP(remoteIP), Port: 12345},
	})
	return metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "bearer "+token))
}

func grpcIPAllowlistStore(serverSecret []byte) *testKeyStore {
	return &testKeyStore{keys: map[string]*KeyRecord{
		"kip1": {
			ID:         "id_ip1",
			OrgID:      "org_1",
			ProjectID:  "proj_1",
			KeyID:      "kip1",
			SecretHash: HashSecret("testsecret", serverSecret),
			Kind:       KeyKindSecret,
			Roles:      []Role{RoleIngestServer},
			AllowedIPs: []string{"203.0.113.0/24"},
		},
	}}
}

// TestGRPCAuthenticate_AllowedIPsEnforced is a regression test for the defect
// where the gRPC AuthContext literal omitted AllowedIPs, leaving the IP check
// dead and letting any IP use a per-key IP-allowlisted credential over gRPC.
func TestGRPCAuthenticate_AllowedIPsEnforced(t *testing.T) {
	serverSecret := []byte("test-server-secret")
	store := grpcIPAllowlistStore(serverSecret)
	cache := NewMemoryKeyCache(10*time.Second, 5*time.Second)
	defer cache.Close()
	rateLimiter := NewKeyRateLimiter()

	t.Run("disallowed IP rejected", func(t *testing.T) {
		ctx := grpcAuthContext("198.51.100.7", "lz_sec_live_kip1_testsecret")
		ac, err := grpcAuthenticate(ctx, store, cache, serverSecret, rateLimiter, false, nil)
		if ac != nil || status.Code(err) != codes.PermissionDenied {
			t.Fatalf("ac=%v err=%v; want nil context and PermissionDenied", ac, err)
		}
	})

	t.Run("allowed IP accepted and allowlist propagated", func(t *testing.T) {
		ctx := grpcAuthContext("203.0.113.42", "lz_sec_live_kip1_testsecret")
		ac, err := grpcAuthenticate(ctx, store, cache, serverSecret, rateLimiter, false, nil)
		if err != nil || ac == nil {
			t.Fatalf("want authenticated context, got ac=%v err=%v", ac, err)
		}
		if len(ac.AllowedIPs) == 0 {
			t.Fatal("AuthContext did not carry AllowedIPs from the key record")
		}
	})
}
