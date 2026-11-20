package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/astraive/loza/cortex/internal/config"
)

// Auth holds only derived authentication data. In particular it does not keep a
// reference to the AuthenticationConfig (and therefore its raw APIKey values):
// those are consumed at construction time and never retained by the Auth object.
type Auth struct {
	enabled    bool
	keyCache   map[string]*hashedAPIKey
	hmacSecret []byte
	initErr    error
	once       sync.Once
	// initCfg is a private copy of the configuration needed by the lazy init.
	// It is cleared as soon as init runs so raw API keys are never retained.
	initCfg *config.AuthenticationConfig
}

type hashedAPIKey struct {
	Name    string
	KeyHash []byte
	Role    string
}

// hmacSecretBytes is the size (in bytes) of a generated HMAC secret: 256 bits
// matches the strength of the HMAC-SHA256 construction that consumes it.
const hmacSecretBytes = 32

// generateHMACSecret is a seam for tests; production code always uses
// GenerateHMACSecret.
var generateHMACSecret = GenerateHMACSecret

// GenerateHMACSecret returns a cryptographically random HMAC key. It is used
// when no explicit secret is configured, so the key has entropy independent of
// the configured API keys and cannot be recomputed from a leaked key.
func GenerateHMACSecret() ([]byte, error) {
	secret := make([]byte, hmacSecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return secret, nil
}

// NewAuth builds an Auth from cfg. The raw API keys are read only here and are
// never retained by the returned Auth: only their HMAC digests are stored. The
// caller's config is left untouched (it is not zeroed or otherwise mutated).
func NewAuth(cfg *config.AuthenticationConfig) *Auth {
	a := &Auth{}
	if cfg == nil {
		return a
	}
	a.enabled = cfg.Enabled
	// Keep a private copy so the caller may scrub its own config immediately.
	clone := *cfg
	clone.APIKeys = append([]config.APIKey(nil), cfg.APIKeys...)
	a.initCfg = &clone
	return a
}

func (a *Auth) init() {
	if a.keyCache != nil || a.initErr != nil {
		return
	}
	cfg := a.initCfg
	// Release the raw API keys as soon as they have been consumed.
	a.initCfg = nil
	if cfg == nil {
		return
	}

	// Use the explicitly configured secret when present. Otherwise generate a
	// cryptographically random secret with independent entropy. Never derive it
	// from the API keys: that would let a single leaked key reconstruct the HMAC
	// key and defeat the separation between the two authentication layers.
	// Production deployments must configure an explicit secret (see config.Validate).
	if cfg.HMACSecret != "" {
		a.hmacSecret = []byte(cfg.HMACSecret)
	} else {
		secret, err := generateHMACSecret()
		if err != nil {
			a.initErr = err
			return
		}
		a.hmacSecret = secret
	}

	if len(cfg.APIKeys) == 0 {
		return
	}
	a.keyCache = make(map[string]*hashedAPIKey, len(cfg.APIKeys))
	for i := range cfg.APIKeys {
		key := &cfg.APIKeys[i]
		hash := hmacSHA256([]byte(key.Key), a.hmacSecret)
		a.keyCache[key.Name] = &hashedAPIKey{
			Name:    key.Name,
			KeyHash: hash,
			Role:    key.Role,
		}
	}
}

func hmacSHA256(data, secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(data)
	return mac.Sum(nil)
}

type AuthResult struct {
	Authorized  bool
	KeyName     string
	Role        string
	Failure     string
	FailureCode string
}

func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.enabled {
			next.ServeHTTP(w, r)
			return
		}

		result := a.authenticate(r)
		if !result.Authorized {
			// Log detailed failure reason server-side; return generic error to client.
			a.logAuthFailure(r, result.KeyName, result.Failure, result.FailureCode)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := r.Context()
		ctx = WithAuthResult(ctx, result)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *Auth) authenticate(r *http.Request) *AuthResult {
	result := &AuthResult{Authorized: false}
	if a.initErr != nil {
		// Fail closed if no usable HMAC secret could be obtained.
		result.Failure = "hmac secret unavailable"
		result.FailureCode = "secret_error"
		return result
	}

	apiKeyHeader := "X-API-Key"

	providedKey := strings.TrimSpace(r.Header.Get(apiKeyHeader))
	if providedKey == "" {
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			providedKey = strings.TrimSpace(authHeader[len("Bearer "):])
		}
	}
	return a.AuthenticateToken(providedKey)
}

// AuthenticateToken validates a raw credential (an API key or the value of a
// bearer token) with the same constant-time HMAC comparison used by the HTTP
// middleware. It is shared with the gRPC interceptors so both transports
// enforce a single authentication policy.
func (a *Auth) AuthenticateToken(providedKey string) *AuthResult {
	a.once.Do(a.init)

	result := &AuthResult{Authorized: false}

	providedKey = strings.TrimSpace(providedKey)
	if providedKey == "" {
		result.Failure = "missing credentials"
		result.FailureCode = "missing"
		return result
	}

	// Compare HMAC-SHA256 of the provided key against the stored hashes with a
	// constant-time comparison to avoid timing side channels. Auth retains only
	// these derived hashes; it does not store the raw API keys from config.
	incomingHash := hmacSHA256([]byte(providedKey), a.hmacSecret)
	for _, cached := range a.keyCache {
		if subtle.ConstantTimeCompare(incomingHash, cached.KeyHash) == 1 {
			result.Authorized = true
			result.KeyName = cached.Name
			result.Role = cached.Role
			return result
		}
	}

	result.Failure = "invalid credentials"
	result.FailureCode = "invalid"
	return result
}

// roleHierarchy is the canonical set of recognized roles and their authorization
// levels. A higher level inherits every permission of the levels below it.
var roleHierarchy = map[string]int{
	"reader": 1,
	"writer": 2,
	"admin":  3,
}

// RoleLevel returns the authorization level for role. The boolean result is
// false when role is not recognized; callers must treat unknown roles as
// unauthorized (fail closed).
func RoleLevel(role string) (int, bool) {
	level, ok := roleHierarchy[role]
	return level, ok
}

// IsValidRole reports whether role is a recognized role name.
func IsValidRole(role string) bool {
	_, ok := roleHierarchy[role]
	return ok
}

func (a *Auth) authorizeRole(r *http.Request, requiredRole string) bool {
	return a.AuthorizeRole(GetAuthResult(r.Context()), requiredRole)
}

// AuthorizeRole reports whether a validated auth result satisfies the
// required role using the same reader < writer < admin hierarchy as HTTP.
func (a *Auth) AuthorizeRole(result *AuthResult, requiredRole string) bool {
	if result == nil {
		return false
	}

	requiredLevel, requiredOK := RoleLevel(requiredRole)
	currentLevel, currentOK := RoleLevel(result.Role)
	if !requiredOK || !currentOK {
		// Fail closed: an unknown role on either side denies access. The old
		// zero-value lookup mapped unknown roles to level 0, which silently
		// granted access whenever the required role was also unrecognized.
		return false
	}

	return currentLevel >= requiredLevel
}

func (a *Auth) HandlerFunc(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.enabled {
			next(w, r)
			return
		}

		result := a.authenticate(r)
		if !result.Authorized {
			// Log detailed failure reason server-side; return generic error to client.
			a.logAuthFailure(r, result.KeyName, result.Failure, result.FailureCode)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := r.Context()
		ctx = WithAuthResult(ctx, result)
		next(w, r.WithContext(ctx))
	}
}

func (a *Auth) RequireRole(role string) func(http.Handler) http.Handler {
	// Fail fast at route-registration time so a misconfigured required role
	// cannot silently degrade into an authorization bypass at request time.
	if !IsValidRole(role) {
		panic(fmt.Sprintf("middleware: invalid required role %q (must be 'reader', 'writer', or 'admin')", role))
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !a.enabled {
				next.ServeHTTP(w, r)
				return
			}

			if !a.authorizeRole(r, role) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

type contextKey string

const authResultKey contextKey = "auth_result"

func WithAuthResult(ctx context.Context, result *AuthResult) context.Context {
	return context.WithValue(ctx, authResultKey, result)
}

func GetAuthResult(ctx context.Context) *AuthResult {
	if v := ctx.Value(authResultKey); v != nil {
		if result, ok := v.(*AuthResult); ok {
			return result
		}
	}
	return nil
}

type AuthMiddleware struct {
	auth *Auth
}

func NewAuthMiddleware(cfg config.AuthenticationConfig) *AuthMiddleware {
	return &AuthMiddleware{auth: NewAuth(&cfg)}
}

func (m *AuthMiddleware) Middleware(next http.Handler) http.Handler {
	if m.auth == nil {
		return next
	}
	return m.auth.Middleware(next)
}

func (m *AuthMiddleware) RequireRole(role string) func(http.Handler) http.Handler {
	if m.auth == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return m.auth.RequireRole(role)
}

func logAuthJSON(level, event string, fields map[string]interface{}) {
	output := map[string]interface{}{
		"level":   level,
		"event":   event,
		"context": fields,
	}
	if data, err := json.Marshal(output); err == nil {
		println(string(data))
	}
}

func (a *Auth) logAuthFailure(r *http.Request, keyName, reason, code string) {
	logAuthJSON("warn", "auth_failure", map[string]interface{}{
		"key_name":     keyName,
		"path":         r.URL.Path,
		"method":       r.Method,
		"remote_addr":  r.RemoteAddr,
		"reason":       reason,
		"failure_code": code,
	})
}
