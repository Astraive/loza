package middleware

import (
	"context"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// GRPCAuthInterceptor enforces the same authentication, role and rate-limit
// policy on gRPC calls that the HTTP middleware enforces on REST routes. It is
// installed as both a unary and a stream interceptor at gRPC server
// construction so every registered RPC is protected by default.
type GRPCAuthInterceptor struct {
	auth      *Auth
	rateLimit *RateLimiter
}

func NewGRPCAuthInterceptor(auth *Auth, rateLimit *RateLimiter) *GRPCAuthInterceptor {
	return &GRPCAuthInterceptor{auth: auth, rateLimit: rateLimit}
}

// grpcMethodRoles mirrors the HTTP route role checks:
//   - ingest and feedback methods require "writer" (POST /events, /feedback/*)
//   - read/query/stream methods require "reader" (reconstruct, graph, /ws)
//
// Unknown methods default to "reader" below, which still requires a valid
// credential and therefore denies unauthenticated calls by default.
var grpcMethodRoles = map[string]string{
	"Healthz":        "reader",
	"IngestEvent":    "writer",
	"IngestBatch":    "writer",
	"Reconstruct":    "reader",
	"GetGraph":       "reader",
	"RecordFeedback": "writer",
	"StreamEvents":   "reader",
	"QueryLifecycle": "reader",
}

func requiredGRPCRole(fullMethod string) string {
	name := fullMethod
	if i := strings.LastIndex(fullMethod, "/"); i >= 0 {
		name = fullMethod[i+1:]
	}
	if role, ok := grpcMethodRoles[name]; ok {
		return role
	}
	return "reader"
}

func (i *GRPCAuthInterceptor) authorize(ctx context.Context, fullMethod string) (context.Context, error) {
	// Authentication is only bypassed when it is explicitly disabled. When it
	// is enabled, missing or invalid credentials are denied by default.
	if i.auth != nil && i.auth.cfg != nil && i.auth.cfg.Enabled {
		result := i.auth.AuthenticateToken(grpcCredential(ctx))
		if !result.Authorized {
			return ctx, status.Error(codes.Unauthenticated, "unauthorized")
		}
		if !i.auth.AuthorizeRole(result, requiredGRPCRole(fullMethod)) {
			return ctx, status.Error(codes.PermissionDenied, "forbidden")
		}
		ctx = WithAuthResult(ctx, result)
	}

	// Rate limiting applies whenever configured, independent of auth, matching
	// the HTTP route group behavior.
	if i.rateLimit != nil && !i.rateLimit.Allow(grpcRateLimitKey(ctx), grpcPeerIP(ctx)) {
		return ctx, status.Error(codes.ResourceExhausted, "rate limit exceeded")
	}

	return ctx, nil
}

// Unary is a grpc.UnaryServerInterceptor.
func (i *GRPCAuthInterceptor) Unary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	fullMethod := ""
	if info != nil {
		fullMethod = info.FullMethod
	}
	authorizedCtx, err := i.authorize(ctx, fullMethod)
	if err != nil {
		return nil, err
	}
	return handler(authorizedCtx, req)
}

// Stream is a grpc.StreamServerInterceptor.
func (i *GRPCAuthInterceptor) Stream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	fullMethod := ""
	if info != nil {
		fullMethod = info.FullMethod
	}
	authorizedCtx, err := i.authorize(ss.Context(), fullMethod)
	if err != nil {
		return err
	}
	return handler(srv, &authServerStream{ServerStream: ss, ctx: authorizedCtx})
}

type authServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authServerStream) Context() context.Context { return s.ctx }

// grpcCredential extracts an API key from incoming gRPC metadata, accepting
// either `x-api-key: <key>` or `authorization: Bearer <key>`, mirroring the
// HTTP middleware's X-API-Key/Authorization handling.
func grpcCredential(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if key := firstMetadataValue(md, "x-api-key"); key != "" {
		return key
	}
	header := firstMetadataValue(md, "authorization")
	if len(header) >= 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

// grpcRateLimitKey matches the HTTP rate limiter, which keys on X-API-Key or
// falls back to the raw Authorization header value.
func grpcRateLimitKey(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if key := firstMetadataValue(md, "x-api-key"); key != "" {
		return key
	}
	return firstMetadataValue(md, "authorization")
}

func firstMetadataValue(md metadata.MD, key string) string {
	values := md.Get(key)
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

// grpcPeerIP returns the client IP from the peer address for per-IP limiting.
func grpcPeerIP(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return ""
	}
	addr := p.Addr.String()
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
