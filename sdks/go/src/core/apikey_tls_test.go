package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression for MASS-0401: a Bearer API key must never be sent to a remote
// plaintext HTTP endpoint. The TLS guard used to be gated only on DSNUsername,
// so a config with just APIKey could build "Authorization: Bearer <key>" over
// http:// without any validation error.

func TestConfigValidate_RejectsRemotePlaintextAPIKey(t *testing.T) {
	cfg := Config{
		CollectorURL: "http://collector.example.com",
		Service:      "checkout",
		APIKey:       "lz_sec_live_secret",
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "require TLS") {
		t.Fatalf("Validate() error = %v, want plaintext API-key rejection", err)
	}
}

func TestConfigValidate_AllowsTLSAPIKey(t *testing.T) {
	cfg := Config{
		CollectorURL: "https://collector.example.com",
		Service:      "checkout",
		APIKey:       "lz_sec_live_secret",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want TLS API key to be accepted", err)
	}
}

func TestConfigValidate_AllowsLocalPlaintextAPIKeyWithInsecure(t *testing.T) {
	cfg := Config{
		CollectorURL: "http://localhost:9308",
		Service:      "checkout",
		APIKey:       "lz_sec_live_secret",
		Insecure:     true,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want Insecure local API key to be accepted", err)
	}
}

func TestConfigValidate_RejectsRemotePlaintextAPIKeyEvenWithInsecure(t *testing.T) {
	cfg := Config{
		CollectorURL: "http://collector.example.com",
		Service:      "checkout",
		APIKey:       "lz_sec_live_secret",
		Insecure:     true,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted a remote plaintext API-key endpoint with Insecure=true")
	}
}

func TestNewClient_RejectsRemotePlaintextAPIKey(t *testing.T) {
	_, err := NewClient(Config{
		CollectorURL: "http://collector.example.com",
		Service:      "checkout",
		APIKey:       "lz_sec_live_secret",
	})
	if err == nil || !strings.Contains(err.Error(), "require TLS") {
		t.Fatalf("NewClient() error = %v, want plaintext API-key rejection", err)
	}
}

func TestCollectorClient_RejectsRemotePlaintextAPIKey(t *testing.T) {
	client := NewCollectorClient(CollectorClientConfig{
		Endpoint: "http://collector.example.com",
		APIKey:   "lz_sec_live_secret",
	})
	_, err := client.Ingest(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "require TLS") {
		t.Fatalf("Ingest() error = %v, want plaintext API-key rejection", err)
	}
}

func TestCollectorClient_SendsAPIKeyOverInsecureLocalEndpoint(t *testing.T) {
	var gotAuthorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	client := NewCollectorClient(CollectorClientConfig{
		Endpoint: srv.URL,
		APIKey:   "lz_sec_live_secret",
		Insecure: true,
		Client:   srv.Client(),
	})
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if gotAuthorization != "Bearer lz_sec_live_secret" {
		t.Fatalf("Authorization = %q, want Bearer API key", gotAuthorization)
	}
}

func TestHTTPBatchSink_RejectsRemotePlaintextBearerHeader(t *testing.T) {
	_, err := HTTPBatchSink(HTTPBatchSinkConfig{
		Endpoint: "http://collector.example.com/events",
		Headers:  map[string]string{"Authorization": "Bearer lz_sec_live_secret"},
	})
	if err == nil || !strings.Contains(err.Error(), "require TLS") {
		t.Fatalf("HTTPBatchSink() error = %v, want plaintext Bearer-header rejection", err)
	}
}

func TestCollectorSink_RejectsRemotePlaintextBearerHeader(t *testing.T) {
	_, err := CollectorSink(CollectorSinkConfig{
		Endpoint: "http://collector.example.com/events",
		Headers:  map[string]string{"authorization": "Bearer lz_sec_live_secret"},
	})
	if err == nil || !strings.Contains(err.Error(), "require TLS") {
		t.Fatalf("CollectorSink() error = %v, want plaintext Bearer-header rejection", err)
	}
}
