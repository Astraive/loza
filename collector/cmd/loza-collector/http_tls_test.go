package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTestClientCAFile writes a self-signed CA certificate to a temporary file
// and returns its path.
func writeTestClientCAFile(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "loza test client CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create ca certificate: %v", err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write ca file: %v", err)
	}
	return path
}

func TestConfigureHTTPServerTLSRequiresCAWhenVerifyEnabled(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*collectorConfig)
	}{
		{
			name: "identity mode mtls",
			setup: func(cfg *collectorConfig) {
				cfg.identityMode = "mtls"
			},
		},
		{
			name: "require client cert",
			setup: func(cfg *collectorConfig) {
				cfg.serverConfig.HTTP.TLSRequireClientCert = true
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &http.Server{}
			cfg := testCollectorConfig()
			cfg.serverConfig.HTTP.TLSEnabled = true
			cfg.serverConfig.HTTP.TLSClientCAFile = ""
			tt.setup(&cfg)

			err := configureHTTPServerTLS(server, cfg)
			if err == nil {
				t.Fatal("expected an error when client cert verification is enabled without a client CA file")
			}
			// Fail closed: no TLS config that could fall back to system roots
			// should be installed on the server.
			if server.TLSConfig != nil && server.TLSConfig.ClientAuth != tls.NoClientCert {
				t.Fatalf("expected client auth to remain unset, got %v", server.TLSConfig.ClientAuth)
			}
		})
	}
}

func TestConfigureHTTPServerTLSWithCAFileAndMTLS(t *testing.T) {
	server := &http.Server{}
	cfg := testCollectorConfig()
	cfg.identityMode = "mtls"
	cfg.serverConfig.HTTP.TLSEnabled = true
	cfg.serverConfig.HTTP.TLSClientCAFile = writeTestClientCAFile(t)

	if err := configureHTTPServerTLS(server, cfg); err != nil {
		t.Fatalf("configure tls: %v", err)
	}
	if server.TLSConfig == nil {
		t.Fatal("expected TLS config to be set")
	}
	if server.TLSConfig.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("expected RequireAndVerifyClientCert, got %v", server.TLSConfig.ClientAuth)
	}
	if server.TLSConfig.ClientCAs == nil {
		t.Fatal("expected client CA pool to be set")
	}
}

func TestConfigureHTTPServerTLSDisabled(t *testing.T) {
	server := &http.Server{}
	cfg := testCollectorConfig()
	cfg.identityMode = "mtls"
	cfg.serverConfig.HTTP.TLSEnabled = false
	cfg.serverConfig.HTTP.TLSClientCAFile = ""

	if err := configureHTTPServerTLS(server, cfg); err != nil {
		t.Fatalf("unexpected error with TLS disabled: %v", err)
	}
	if server.TLSConfig != nil {
		t.Fatalf("expected no TLS config when TLS is disabled, got %+v", server.TLSConfig)
	}
}
