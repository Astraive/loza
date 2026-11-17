package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	testJWTSecret   = "0123456789abcdef0123456789abcdef"
	testJWTIssuer   = "https://issuer.loza.test"
	testJWTAudience = "loza-collector"
	testJWTSubject  = "ingest-service"
)

func signTestJWT(t *testing.T, secret string, claims jwt.Claims) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte(secret)}, nil)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("serialize jwt: %v", err)
	}
	return token
}

func jwtAuthorizeState(cfg collectorConfig) *collectorState {
	cfg.authEnabled = true
	cfg.identityMode = "jwt"
	cfg.apiKey = testJWTSecret
	return &collectorState{cfg: cfg}
}

func TestAuthorizeJWTBindsConfiguredClaims(t *testing.T) {
	validClaims := func() jwt.Claims {
		return jwt.Claims{
			Issuer:   testJWTIssuer,
			Audience: jwt.Audience{testJWTAudience},
			Subject:  testJWTSubject,
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
		}
	}

	tests := []struct {
		name          string
		expectedSub   string
		mutate        func(*jwt.Claims)
		wantAuthorize bool
	}{
		{name: "valid issuer audience and subject", expectedSub: testJWTSubject, wantAuthorize: true},
		{name: "subject optional when not configured", wantAuthorize: true},
		{
			name: "wrong issuer",
			mutate: func(c *jwt.Claims) {
				c.Issuer = "https://attacker.test"
			},
		},
		{
			name: "missing issuer",
			mutate: func(c *jwt.Claims) {
				c.Issuer = ""
			},
		},
		{
			name: "wrong audience",
			mutate: func(c *jwt.Claims) {
				c.Audience = jwt.Audience{"some-other-service"}
			},
		},
		{
			name: "missing audience",
			mutate: func(c *jwt.Claims) {
				c.Audience = nil
			},
		},
		{
			name:        "wrong subject",
			expectedSub: testJWTSubject,
			mutate: func(c *jwt.Claims) {
				c.Subject = "attacker"
			},
		},
		{
			name: "expired token",
			mutate: func(c *jwt.Claims) {
				c.Expiry = jwt.NewNumericDate(time.Now().Add(-time.Hour))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := validClaims()
			if tt.mutate != nil {
				tt.mutate(&claims)
			}
			token := signTestJWT(t, testJWTSecret, claims)

			cfg := testCollectorConfig()
			cfg.jwtIssuer = testJWTIssuer
			cfg.jwtAudience = []string{testJWTAudience}
			cfg.jwtSubject = tt.expectedSub
			state := jwtAuthorizeState(cfg)

			req := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+token)
			if got := state.authorizeJWT(req); got != tt.wantAuthorize {
				t.Fatalf("authorizeJWT() = %v, want %v", got, tt.wantAuthorize)
			}
		})
	}
}

func TestAuthorizeJWTRejectsTokensWhenExpectedClaimsUnconfigured(t *testing.T) {
	token := signTestJWT(t, testJWTSecret, jwt.Claims{
		Issuer:   testJWTIssuer,
		Audience: jwt.Audience{testJWTAudience},
		Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})

	tests := []struct {
		name   string
		issuer string
		aud    []string
	}{
		{name: "missing issuer"},
		{name: "missing audience", issuer: testJWTIssuer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testCollectorConfig()
			cfg.jwtIssuer = tt.issuer
			cfg.jwtAudience = tt.aud
			state := jwtAuthorizeState(cfg)

			req := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+token)
			if state.authorizeJWT(req) {
				t.Fatal("authorizeJWT() accepted a token without configured issuer and audience")
			}
		})
	}
}
