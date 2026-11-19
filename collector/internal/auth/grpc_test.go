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

func TestGRPCAuthenticatePropagatesCollectorGrants(t *testing.T) {
	serverSecret := []byte("test-server-secret")
	keyID := "kgrpc1"
	store := &testKeyStore{
		keys: map[string]*KeyRecord{
			keyID: {
				ID:         "id_grpc1",
				OrgID:      "org_1",
				ProjectID:  "proj_1",
				KeyID:      keyID,
				SecretHash: HashSecret("testsecret", serverSecret),
				Kind:       KeyKindSecret,
				Roles:      []Role{RoleIngestServer},
				CollectorGrants: []CollectorGrant{{
					Collector:    "payments",
					Environments: []string{"prod"},
					Permissions:  map[Permission]bool{PermEventsWrite: true},
				}},
			},
		},
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"authorization", "Bearer lz_sec_live_"+keyID+"_testsecret",
	))
	ac, err := grpcAuthenticate(ctx, store, nil, serverSecret, nil, false, nil)
	if err != nil {
		t.Fatalf("grpcAuthenticate() unexpected error: %v", err)
	}
	if ac == nil {
		t.Fatal("grpcAuthenticate() returned nil auth context")
	}

	if !ac.AuthorizesCollector("payments", "prod", PermEventsWrite) {
		t.Fatal("gRPC auth context must authorize the configured collector grant")
	}
	if ac.AuthorizesCollector("analytics", "prod", PermEventsWrite) {
		t.Fatal("gRPC auth context must deny a different collector")
	}
	if ac.AuthorizesCollector("payments", "prod", PermEventsRead) {
		t.Fatal("gRPC auth context must deny an ungranted permission")
	}

	// Grants must be defensively cloned, matching the HTTP middleware path,
	// so mutating the stored record cannot change authorization decisions.
	store.keys[keyID].CollectorGrants[0].Permissions[PermEventsWrite] = false
	if !ac.AuthorizesCollector("payments", "prod", PermEventsWrite) {
		t.Fatal("gRPC auth context must not alias the stored collector grant")
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TestGRPCAuthenticate_RejectedLocalDevKeyNotLogged verifies that rejecting a
// local dev key does not write any of the raw credential (including the secret
// component) to the logs. Only the rejection reason should be emitted, matching
// the HTTP middleware path.
func TestGRPCAuthenticate_RejectedLocalDevKeyNotLogged(t *testing.T) {
	const localKey = "lz_local_dev_LEAKMEsecretmaterial"
	// The raw key's first 20 chars are "lz_local_dev_LEAKMEs",
	// which previously leaked the "LEAKMEs" secret fragment to logs.

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"authorization", "Bearer "+localKey,
	))

	_, err := grpcAuthenticate(ctx, &testKeyStore{}, nil, []byte("test-server-secret"), NewKeyRateLimiter(), false, nil)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("error code = %v, want %v (err=%v)", status.Code(err), codes.PermissionDenied, err)
	}

	output := logs.String()
	if !strings.Contains(output, "local dev key rejected") {
		t.Fatalf("expected rejection reason in logs, got %q", output)
	}
	// No portion of the raw credential or its secret may be logged.
	for _, leaked := range []string{localKey, "LEAKMEsecretmaterial", "LEAKMEs", "key="} {
		if strings.Contains(output, leaked) {
			t.Errorf("log output contains key material %q: %q", leaked, output)
		}
	}
}
