package middleware

import (
	"context"
	"net"
	"testing"

	"github.com/astraive/loza/cortex/internal/config"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

var allGRPCMethods = []string{
	"Healthz", "IngestEvent", "IngestBatch", "Reconstruct", "GetGraph",
	"RecordFeedback", "StreamEvents", "QueryLifecycle",
}

var writerGRPCMethods = map[string]bool{
	"IngestEvent": true, "IngestBatch": true, "RecordFeedback": true,
}

func testFullMethod(name string) string {
	return "/loza.core.CortexService/" + name
}

// testContext builds an incoming gRPC context with optional credentials and a
// deterministic peer address so per-IP rate limiting can be exercised.
func testContext(apiKey, authHeader string) context.Context {
	md := metadata.MD{}
	if apiKey != "" {
		md.Set("x-api-key", apiKey)
	}
	if authHeader != "" {
		md.Set("authorization", authHeader)
	}
	ctx := metadata.NewIncomingContext(context.Background(), md)
	addr, _ := net.ResolveTCPAddr("tcp", "127.0.0.1:12345")
	return peer.NewContext(ctx, &peer.Peer{Addr: addr})
}

type testServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *testServerStream) Context() context.Context { return s.ctx }

func testAuthConfig() config.AuthenticationConfig {
	return config.AuthenticationConfig{
		Enabled: true,
		APIKeys: []config.APIKey{
			{Name: "reader-key", Key: "reader-secret", Role: "reader"},
			{Name: "writer-key", Key: "writer-secret", Role: "writer"},
			{Name: "admin-key", Key: "admin-secret", Role: "admin"},
		},
	}
}

func callUnary(i *GRPCAuthInterceptor, ctx context.Context, method string) (codes.Code, *AuthResult) {
	var seen *AuthResult
	_, err := i.Unary(ctx, nil, &grpc.UnaryServerInfo{FullMethod: testFullMethod(method)},
		func(ctx context.Context, _ any) (any, error) {
			seen = GetAuthResult(ctx)
			return "ok", nil
		})
	return status.Code(err), seen
}

func callStream(i *GRPCAuthInterceptor, ctx context.Context, method string) (codes.Code, *AuthResult) {
	var seen *AuthResult
	err := i.Stream(nil, &testServerStream{ctx: ctx}, &grpc.StreamServerInfo{FullMethod: testFullMethod(method)},
		func(_ any, stream grpc.ServerStream) error {
			seen = GetAuthResult(stream.Context())
			return nil
		})
	return status.Code(err), seen
}

func TestGRPCAuthPolicyCoversAllRPCs(t *testing.T) {
	for _, method := range allGRPCMethods {
		require.Contains(t, grpcMethodRoles, method, "missing gRPC auth policy for %s", method)
	}
	require.Equal(t, "reader", requiredGRPCRole(testFullMethod("Reconstruct")))
	require.Equal(t, "writer", requiredGRPCRole(testFullMethod("IngestEvent")))
	require.Equal(t, "reader", requiredGRPCRole("/unknown.Service/Unknown"))
}

func TestGRPCAuthDisabledAllowsAllRPCs(t *testing.T) {
	interceptor := NewGRPCAuthInterceptor(NewAuth(&config.AuthenticationConfig{}), nil)
	for _, method := range allGRPCMethods {
		ctx := testContext("", "")
		code, seen := callUnary(interceptor, ctx, method)
		require.Equal(t, codes.OK, code, "unary %s", method)
		require.Nil(t, seen, "no auth result when disabled: %s", method)

		code, seen = callStream(interceptor, ctx, method)
		require.Equal(t, codes.OK, code, "stream %s", method)
		require.Nil(t, seen, "no auth result when disabled: %s", method)
	}
}

func TestGRPCAuthRejectsUnauthenticatedAndWrongKey(t *testing.T) {
	interceptor := NewGRPCAuthInterceptor(NewAuth(testAuthConfigPtr()), nil)

	tests := []struct {
		name string
		ctx  context.Context
	}{
		{name: "missing credentials", ctx: testContext("", "")},
		{name: "wrong key", ctx: testContext("not-a-key", "")},
		{name: "wrong bearer token", ctx: testContext("", "Bearer not-a-key")},
		{name: "non-bearer authorization", ctx: testContext("", "writer-secret")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, method := range allGRPCMethods {
				code, seen := callUnary(interceptor, tc.ctx, method)
				require.Equal(t, codes.Unauthenticated, code, "unary %s", method)
				require.Nil(t, seen, "handler must not run for %s", method)

				code, seen = callStream(interceptor, tc.ctx, method)
				require.Equal(t, codes.Unauthenticated, code, "stream %s", method)
				require.Nil(t, seen, "handler must not run for %s", method)
			}
		})
	}
}

func TestGRPCAuthEnforcesRolesForEveryRPC(t *testing.T) {
	interceptor := NewGRPCAuthInterceptor(NewAuth(testAuthConfigPtr()), nil)

	roles := []struct {
		role string
		key  string
	}{
		{role: "reader", key: "reader-secret"},
		{role: "writer", key: "writer-secret"},
		{role: "admin", key: "admin-secret"},
	}

	for _, method := range allGRPCMethods {
		for _, tc := range roles {
			want := codes.OK
			if writerGRPCMethods[method] && tc.role == "reader" {
				want = codes.PermissionDenied
			}

			ctx := testContext(tc.key, "")
			code, seen := callUnary(interceptor, ctx, method)
			require.Equal(t, want, code, "unary %s with %s role", method, tc.role)
			if want == codes.OK {
				require.NotNil(t, seen, "auth result propagated to %s handler", method)
				require.Equal(t, tc.role, seen.Role)
			} else {
				require.Nil(t, seen, "handler must not run for %s with %s role", method, tc.role)
			}

			code, seen = callStream(interceptor, ctx, method)
			require.Equal(t, want, code, "stream %s with %s role", method, tc.role)
			if want == codes.OK {
				require.NotNil(t, seen, "auth result propagated to %s stream handler", method)
				require.Equal(t, tc.role, seen.Role)
			} else {
				require.Nil(t, seen, "handler must not run for %s with %s role", method, tc.role)
			}
		}
	}
}

func TestGRPCAuthAcceptsBearerAndAPIKeyMetadata(t *testing.T) {
	interceptor := NewGRPCAuthInterceptor(NewAuth(testAuthConfigPtr()), nil)

	tests := []struct {
		name       string
		ctx        context.Context
		wantResult string
	}{
		{name: "x-api-key", ctx: testContext("writer-secret", ""), wantResult: "writer-key"},
		{name: "bearer token", ctx: testContext("", "Bearer writer-secret"), wantResult: "writer-key"},
		{name: "lowercase bearer", ctx: testContext("", "bearer writer-secret"), wantResult: "writer-key"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, seen := callUnary(interceptor, tc.ctx, "IngestEvent")
			require.Equal(t, codes.OK, code)
			require.Equal(t, tc.wantResult, seen.KeyName)
			require.Equal(t, "writer", seen.Role)
		})
	}
}

func TestGRPCAuthUnknownMethodDeniesByDefault(t *testing.T) {
	interceptor := NewGRPCAuthInterceptor(NewAuth(testAuthConfigPtr()), nil)

	_, err := interceptor.Unary(testContext("", ""), nil,
		&grpc.UnaryServerInfo{FullMethod: "/loza.core.CortexService/FutureRPC"},
		func(context.Context, any) (any, error) { return "ok", nil })
	require.Equal(t, codes.Unauthenticated, status.Code(err))

	code, _ := callUnary(interceptor, testContext("reader-secret", ""), "FutureRPC")
	require.Equal(t, codes.OK, code, "unknown methods still accept any valid credential")
}

func TestGRPCAuthRateLimitMatchesHTTPPolicy(t *testing.T) {
	cfg := testAuthConfigPtr()
	// 1 RPM and 1 RPM per IP both yield a burst of one, so the second call
	// from the same key/IP must be rejected.
	rateLimit := NewRateLimiter(1, 1)
	interceptor := NewGRPCAuthInterceptor(NewAuth(cfg), rateLimit)

	ctx := testContext("writer-secret", "")
	code, _ := callUnary(interceptor, ctx, "IngestEvent")
	require.Equal(t, codes.OK, code, "first call allowed")

	code, seen := callUnary(interceptor, ctx, "IngestEvent")
	require.Equal(t, codes.ResourceExhausted, code, "second call rate limited")
	require.Nil(t, seen, "handler must not run when rate limited")

	// Rate limiting is applied even when authentication is disabled, matching
	// the HTTP route group.
	noAuth := NewGRPCAuthInterceptor(NewAuth(&config.AuthenticationConfig{}), NewRateLimiter(1, 1))
	code, _ = callUnary(noAuth, testContext("any-key", ""), "Healthz")
	require.Equal(t, codes.OK, code)
	code, _ = callUnary(noAuth, testContext("any-key", ""), "Healthz")
	require.Equal(t, codes.ResourceExhausted, code)
}

// testAuthConfigPtr returns a fresh pointer so each interceptor owns its own
// initialized Auth state.
func testAuthConfigPtr() *config.AuthenticationConfig {
	cfg := testAuthConfig()
	return &cfg
}
