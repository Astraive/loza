package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/astraive/loza/cortex/internal/config"
)

// Auth holds only derived authentication data. In particular it does not keep a
// reference to the AuthenticationConfig (and therefore its raw APIKey values):
// those are consumed at construction time and never retained by the Auth object.
type Auth struct {
	enabled    bool
	keyCache   map[string]*hashedAPIKey
	hmacSecret []byte
}

type hashedAPIKey struct {
	Name    string
	KeyHash []byte
	Role    string
}

// AutoGenerateHMACSecret derives a deterministic HMAC key from the configured API keys.
// This ensures the same config always produces the same secret without requiring
// an explicit HMAC_SECRET env var. Because it is derived only from the same keys it
// protects, it adds no independent secret entropy; set HMACSecret to supply one.
func AutoGenerateHMACSecret(keys []config.APIKey) []byte {
	var parts []string
	for _, k := range keys {
		parts = append(parts, k.Key)
	}
	sort.Strings(parts)
	combined := strings.Join(parts, "|")
	sum := sha256.Sum256([]byte(combined))
	return sum[:]
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
	if len(cfg.APIKeys) == 0 {
		return a
	if a.cfg == nil || len(a.cfg.APIKeys) == 0 {
		return
	}

	// Determine HMAC secret: use explicit config or auto-generate from API keys.
	if cfg.HMACSecret != "" {
		a.hmacSecret = []byte(cfg.HMACSecret)
	} else {
		a.hmacSecret = AutoGenerateHMACSecret(cfg.APIKeys)
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
	return a
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

	apiKeyHeader := "X-API-Key"

	providedKey := strings.TrimSpace(r.Header.Get(apiKeyHeader))
	providedKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
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

func (a *Auth) authorizeRole(r *http.Request, requiredRole string) bool {
	return a.AuthorizeRole(GetAuthResult(r.Context()), requiredRole)
}

// AuthorizeRole reports whether a validated auth result satisfies the
// required role using the same reader < writer < admin hierarchy as HTTP.
func (a *Auth) AuthorizeRole(result *AuthResult, requiredRole string) bool {
	if result == nil {
		return false
	}

	roleHierarchy := map[string]int{
		"reader": 1,
		"writer": 2,
		"admin":  3,
	}

	requiredLevel, ok := roleHierarchy[requiredRole]
	if !ok {
		// An unknown or empty required role is a configuration/caller error;
		// never treat it as level 0 and thereby authorize everyone.
		return false
	}
	currentLevel, ok := roleHierarchy[result.Role]
	if !ok || currentLevel == 0 {
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
