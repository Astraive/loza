package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTLSFiles writes a self-signed CA, a server certificate/key and a client
// certificate/key to a temporary directory and returns their paths.
func newTLSFiles(t *testing.T) (certFile, keyFile, caFile, clientCertFile, clientKeyFile string) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "loza-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	leaf := func(serial int64, cn string, usage x509.ExtKeyUsage) ([]byte, []byte) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: cn},
			DNSNames:     []string{"localhost"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
			pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	}

	serverCert, serverKey := leaf(2, "localhost", x509.ExtKeyUsageServerAuth)
	clientCert, clientKey := leaf(3, "loza-test-client", x509.ExtKeyUsageClientAuth)

	dir := t.TempDir()
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	certFile = write("server.pem", serverCert)
	keyFile = write("server-key.pem", serverKey)
	caFile = write("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	clientCertFile = write("client.pem", clientCert)
	clientKeyFile = write("client-key.pem", clientKey)
	return certFile, keyFile, caFile, clientCertFile, clientKeyFile
}

func TestBuildTLSConfigDisabled(t *testing.T) {
	cfg, err := BuildTLSConfig(TLSConfig{Enabled: false})
	if err != nil {
		t.Fatalf("disabled TLS must not error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("disabled TLS must return a nil config, got %+v", cfg)
	}
}

func TestBuildTLSConfigEnforcesMinVersion(t *testing.T) {
	certFile, keyFile, _, _, _ := newTLSFiles(t)
	for _, tc := range []struct {
		name string
		min  string
		want uint16
	}{
		{name: "default", min: "", want: tls.VersionTLS12},
		{name: "tls12", min: "1.2", want: tls.VersionTLS12},
		{name: "tls13", min: "1.3", want: tls.VersionTLS13},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := BuildTLSConfig(TLSConfig{
				Enabled: true, CertFile: certFile, KeyFile: keyFile, MinVersion: tc.min,
			})
			if err != nil {
				t.Fatalf("BuildTLSConfig: %v", err)
			}
			if cfg.MinVersion != tc.want {
				t.Fatalf("MinVersion = %x, want %x", cfg.MinVersion, tc.want)
			}
			if len(cfg.Certificates) != 1 {
				t.Fatalf("expected preloaded certificate chain, got %d certificates", len(cfg.Certificates))
			}
		})
	}

	if _, err := BuildTLSConfig(TLSConfig{
		Enabled: true, CertFile: certFile, KeyFile: keyFile, MinVersion: "1.1",
	}); err == nil {
		t.Fatal("expected an error for an unsupported min_version")
	}
}

func TestBuildTLSConfigMutualTLS(t *testing.T) {
	certFile, keyFile, caFile, _, _ := newTLSFiles(t)
	cfg, err := BuildTLSConfig(TLSConfig{
		Enabled: true, CertFile: certFile, KeyFile: keyFile,
		MinVersion: "1.3", MutualTLS: true, ClientCAFile: caFile,
	})
	if err != nil {
		t.Fatalf("BuildTLSConfig: %v", err)
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("ClientAuth = %v, want RequireAndVerifyClientCert", cfg.ClientAuth)
	}
	if cfg.ClientCAs == nil {
		t.Fatal("client CA pool was not populated")
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion = %x, want %x", cfg.MinVersion, tls.VersionTLS13)
	}
}

func TestBuildTLSConfigRejectsInvalidConfiguration(t *testing.T) {
	certFile, keyFile, _, _, _ := newTLSFiles(t)

	for _, tc := range []struct {
		name string
		cfg  TLSConfig
	}{
		{name: "missing cert", cfg: TLSConfig{Enabled: true, KeyFile: keyFile, MinVersion: "1.2"}},
		{name: "missing key", cfg: TLSConfig{Enabled: true, CertFile: certFile, MinVersion: "1.2"}},
		{name: "unreadable cert", cfg: TLSConfig{Enabled: true, CertFile: "/nonexistent/cert.pem", KeyFile: keyFile, MinVersion: "1.2"}},
		{name: "mtls without ca", cfg: TLSConfig{Enabled: true, CertFile: certFile, KeyFile: keyFile, MinVersion: "1.2", MutualTLS: true}},
		{name: "invalid ca", cfg: TLSConfig{Enabled: true, CertFile: certFile, KeyFile: keyFile, MinVersion: "1.2", MutualTLS: true, ClientCAFile: keyFile}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildTLSConfig(tc.cfg); err == nil {
				t.Fatal("expected an error for invalid TLS configuration")
			}
		})
	}
}
