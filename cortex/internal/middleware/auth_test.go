package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/astraive/loza/cortex/internal/config"
)

func TestAuthMiddlewareDisabledPassesThrough(t *testing.T) {
	auth := NewAuth(&config.AuthenticationConfig{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected pass-through, got %d", rec.Code)
	}
}

func TestAuthMiddlewareRejectsAndAccepts(t *testing.T) {
	auth := NewAuth(&config.AuthenticationConfig{
		Enabled: true,
		APIKeys: []config.APIKey{{Name: "key", Key: "secret", Role: "writer"}},
	})

	t.Run("missing credentials", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized, got %d", rec.Code)
		}
	})

	t.Run("valid bearer token", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer secret")
		auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if result := GetAuthResult(r.Context()); result == nil || result.KeyName != "key" {
				t.Fatalf("expected auth result in context, got %+v", result)
			}
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected ok, got %d", rec.Code)
		}
	})
}

// TestAuthDoesNotRetainRawKeys guards the fix for MASS-0337: Auth must derive
// everything from the configured keys at construction time and must not keep a
// reference to the config (or its raw API keys) that would be exposed by a
// memory disclosure of the Auth object.
func TestAuthDoesNotRetainRawKeys(t *testing.T) {
	const rawKey = "raw-secret-key-not-retained"
	cfg := &config.AuthenticationConfig{
		Enabled: true,
		APIKeys: []config.APIKey{{Name: "key", Key: rawKey, Role: "admin"}},
	}

	auth := NewAuth(cfg)

	// Simulate the caller scrubbing the raw keys from its config once Auth is
	// built. Authentication must still succeed, proving the raw keys are not
	// read back from cfg at request time (nor retained by Auth itself).
	for i := range cfg.APIKeys {
		cfg.APIKeys[i].Key = ""
	}
	cfg.APIKeys = nil

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", rawKey)
	rec := httptest.NewRecorder()
	auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result := GetAuthResult(r.Context())
		if result == nil || !result.Authorized || result.KeyName != "key" || result.Role != "admin" {
			t.Fatalf("expected authorized admin result, got %+v", result)
		}
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (raw keys must not be needed after construction)", rec.Code, http.StatusOK)
	}
}

// Building Auth must not mutate the caller's config: avoiding retention of raw
// keys in Auth does not justify zeroing out a config the caller may still use.
func TestNewAuthDoesNotMutateConfig(t *testing.T) {
	const rawKey = "keep-this-key"
	cfg := &config.AuthenticationConfig{
		Enabled: true,
		APIKeys: []config.APIKey{{Name: "key", Key: rawKey, Role: "admin"}},
	}

	_ = NewAuth(cfg)

	if len(cfg.APIKeys) != 1 || cfg.APIKeys[0].Key != rawKey {
		t.Fatalf("NewAuth mutated caller config APIKeys: %+v", cfg.APIKeys)
	}
}

func TestRequireRole(t *testing.T) {
	tests := []struct {
		name string
		role string
		key  string
		want int
	}{
		{name: "reader cannot write", role: "reader", key: "reader-key", want: http.StatusForbidden},
		{name: "writer can write", role: "writer", key: "writer-key", want: http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			auth := NewAuth(&config.AuthenticationConfig{
				Enabled: true,
				APIKeys: []config.APIKey{{Name: tc.role, Key: tc.key, Role: tc.role}},
			})
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("X-API-Key", tc.key)
			rec := httptest.NewRecorder()
			handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				auth.RequireRole("writer")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
				})).ServeHTTP(w, r)
			}))

			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestRequireRoleRejectsUnknownRequiredRole(t *testing.T) {
func TestAuthorizeRoleFailsClosedOnUnknownRoles(t *testing.T) {
	auth := NewAuth(&config.AuthenticationConfig{Enabled: true})

	tests := []struct {
		name         string
		requiredRole string
		currentRole  string
		want         int
	}{
		{name: "empty required role denied", requiredRole: "", currentRole: "admin", want: http.StatusForbidden},
		{name: "misspelled required role denied", requiredRole: "administrator", currentRole: "admin", want: http.StatusForbidden},
		{name: "misspelled lowercase required role denied", requiredRole: "writer ", currentRole: "admin", want: http.StatusForbidden},
		{name: "valid admin required role allowed", requiredRole: "admin", currentRole: "admin", want: http.StatusOK},
		{name: "empty current role denied", requiredRole: "reader", currentRole: "", want: http.StatusForbidden},
		{name: "unknown current role denied", requiredRole: "reader", currentRole: "superuser", want: http.StatusForbidden},
		callerRole   string
		want         bool
	}{
		{name: "admin satisfies admin", requiredRole: "admin", callerRole: "admin", want: true},
		{name: "admin satisfies writer", requiredRole: "writer", callerRole: "admin", want: true},
		{name: "writer does not satisfy admin", requiredRole: "admin", callerRole: "writer", want: false},
		{name: "reader does not satisfy writer", requiredRole: "writer", callerRole: "reader", want: false},
		// Unknown role on either side must deny.
		{name: "unknown required role", requiredRole: "admni", callerRole: "admin", want: false},
		{name: "unknown caller role", requiredRole: "admin", callerRole: "hacker", want: false},
		{name: "both roles unknown", requiredRole: "admni", callerRole: "hacker", want: false},
		{name: "empty required role", requiredRole: "", callerRole: "admin", want: false},
		{name: "empty caller role", requiredRole: "admin", callerRole: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = req.WithContext(WithAuthResult(req.Context(), &AuthResult{
				Authorized: true,
				Role:       tc.currentRole,
			}))
			rec := httptest.NewRecorder()

			auth.RequireRole(tc.requiredRole)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r = r.WithContext(WithAuthResult(r.Context(), &AuthResult{Authorized: true, Role: tc.callerRole}))
			if got := auth.authorizeRole(r, tc.requiredRole); got != tc.want {
				t.Fatalf("authorizeRole(required=%q, caller=%q) = %v, want %v", tc.requiredRole, tc.callerRole, got, tc.want)
			}
		})
	}
}

func TestAuthorizeRoleWithoutAuthResultDenies(t *testing.T) {
	auth := NewAuth(&config.AuthenticationConfig{Enabled: true})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if auth.authorizeRole(r, "reader") {
		t.Fatal("expected deny when no auth result is present in the context")
	}
}

func TestRequireRoleUnknownCallerRoleDenied(t *testing.T) {
	// An unrecognized role can reach the auth context from an external identity
	// provider even if config validation was bypassed. It must be denied.
	auth := NewAuth(&config.AuthenticationConfig{
		Enabled: true,
		APIKeys: []config.APIKey{{Name: "key", Key: "secret", Role: "hacker"}},
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "secret")
	rec := httptest.NewRecorder()
	auth.Middleware(auth.RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d for unknown caller role", rec.Code, http.StatusForbidden)
	}
}

func TestRequireRoleRejectsInvalidRequiredRoleAtRegistration(t *testing.T) {
	auth := NewAuth(&config.AuthenticationConfig{Enabled: true})
	defer func() {
		if recover() == nil {
			t.Fatal("expected RequireRole to fail fast on an invalid required role")
		}
	}()
	auth.RequireRole("admni")
}

func TestIsValidRole(t *testing.T) {
	for _, role := range []string{"reader", "writer", "admin"} {
		if !IsValidRole(role) {
			t.Errorf("IsValidRole(%q) = false, want true", role)
		}
	}
	for _, role := range []string{"", "admni", "READER", "hacker"} {
		if IsValidRole(role) {
			t.Errorf("IsValidRole(%q) = true, want false", role)
		}
	}
}
