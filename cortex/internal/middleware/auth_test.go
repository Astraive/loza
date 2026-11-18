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
