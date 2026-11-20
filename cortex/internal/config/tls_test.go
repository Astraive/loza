package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- shared test helpers ----
func testCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "loza-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func testLeaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, cn string, usage x509.ExtKeyUsage) ([]byte, []byte, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), cert
}

func writeTestFile(t *testing.T, dir, name string, contents []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func tlsTestPaths(t *testing.T) (certFile, keyFile string, ca *x509.Certificate, caKey *ecdsa.PrivateKey, caPEM []byte) {
	t.Helper()
	dir := t.TempDir()
	ca, caKey, caPEM = testCA(t)
	certPEM, keyPEM, _ := testLeaf(t, ca, caKey, "localhost", x509.ExtKeyUsageServerAuth)
	return writeTestFile(t, dir, "server.crt", certPEM),
		writeTestFile(t, dir, "server.key", keyPEM), ca, caKey, caPEM
}

func TestBuildTLSConfigServesHTTPSAndMTLS(t *testing.T) {
	dir := t.TempDir()
	ca, caKey, caPEM := testCA(t)
	serverCert, serverKey, _ := testLeaf(t, ca, caKey, "localhost", x509.ExtKeyUsageServerAuth)
	clientCert, clientKey, _ := testLeaf(t, ca, caKey, "loza-client", x509.ExtKeyUsageClientAuth)
	certFile := writeTestFile(t, dir, "server.crt", serverCert)
	keyFile := writeTestFile(t, dir, "server.key", serverKey)
	caFile := writeTestFile(t, dir, "ca.crt", caPEM)

	tlsCfg, err := BuildTLSConfig(TLSConfig{
		Enabled:      true,
		CertFile:     certFile,
		KeyFile:      keyFile,
		MinVersion:   "1.3",
		MutualTLS:    true,
		ClientCAFile: caFile,
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}),
		TLSConfig: tlsCfg,
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.ServeTLS(lis, certFile, keyFile) }()
	t.Cleanup(func() { _ = srv.Close() })

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("failed to parse test CA")
	}
	newClient := func(certs []tls.Certificate, maxVersion uint16) *http.Client {
		return &http.Client{
			Timeout: 2 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{
				RootCAs: pool, ServerName: "localhost",
				Certificates: certs, MaxVersion: maxVersion,
			}},
		}
	}
	clientPair, err := tls.X509KeyPair(clientCert, clientKey)
	if err != nil {
		t.Fatal(err)
	}
	addr := "https://" + lis.Addr().String()

	resp, err := newClient([]tls.Certificate{clientPair}, 0).Get(addr)
	if err != nil {
		t.Fatalf("mTLS request with a valid client certificate failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" {
		t.Fatalf("response body = %q, want ok", body)
	}

	if _, err := newClient(nil, 0).Get(addr); err == nil {
		t.Fatal("mTLS listener accepted a client without a certificate")
	}
	if _, err := newClient([]tls.Certificate{clientPair}, tls.VersionTLS12).Get(addr); err == nil {
		t.Fatal("TLS 1.2 client was accepted with min_version 1.3")
	}
}

func TestBuildTLSConfigDisabled(t *testing.T) {
	// Even a deliberately broken configuration must not error or enable TLS
	// when TLS is disabled.
	cfg, err := BuildTLSConfig(TLSConfig{
		Enabled:      false,
		CertFile:     "/does/not/exist",
		KeyFile:      "/does/not/exist",
		MinVersion:   "nonsense",
		MutualTLS:    true,
		ClientCAFile: "/does/not/exist",
	})
	if err != nil {
		t.Fatalf("disabled TLS should not error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("disabled TLS should return a nil config, got %+v", cfg)
	}
}

func TestBuildTLSConfigMinVersions(t *testing.T) {
	certFile, keyFile, _, _, _ := tlsTestPaths(t)
	for _, tc := range []struct {
		name    string
		version string
		want    uint16
	}{
		{name: "default", version: "", want: tls.VersionTLS12},
		{name: "tls12", version: "1.2", want: tls.VersionTLS12},
		{name: "tls13", version: "1.3", want: tls.VersionTLS13},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := BuildTLSConfig(TLSConfig{
				Enabled:    true,
				CertFile:   certFile,
				KeyFile:    keyFile,
				MinVersion: tc.version,
			})
			if err != nil {
				t.Fatalf("BuildTLSConfig() error = %v", err)
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
	certFile, keyFile, ca, caKey, caPEM := tlsTestPaths(t)
	caFile := writeTestFile(t, t.TempDir(), "ca.crt", caPEM)
	_, _, clientCert := testLeaf(t, ca, caKey, "loza-client", x509.ExtKeyUsageClientAuth)

	cfg, err := BuildTLSConfig(TLSConfig{
		Enabled:      true,
		CertFile:     certFile,
		KeyFile:      keyFile,
		MinVersion:   "1.3",
		MutualTLS:    true,
		ClientCAFile: caFile,
	})
	if err != nil {
		t.Fatalf("BuildTLSConfig() error = %v", err)
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
	if _, err := clientCert.Verify(x509.VerifyOptions{
		Roots:     cfg.ClientCAs,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Fatalf("client certificate issued by the configured CA did not verify: %v", err)
	}
}

func TestBuildTLSConfigInvalid(t *testing.T) {
	certFile, keyFile, _, _, caPEM := tlsTestPaths(t)
	badFile := writeTestFile(t, t.TempDir(), "garbage.pem", []byte("not a certificate"))

	for _, tc := range []struct {
		name    string
		cfg     TLSConfig
		wantErr string
	}{
		{
			name:    "missing cert file",
			cfg:     TLSConfig{Enabled: true, KeyFile: keyFile, MinVersion: "1.2"},
			wantErr: "cert_file is required",
		},
		{
			name:    "missing key file",
			cfg:     TLSConfig{Enabled: true, CertFile: certFile, MinVersion: "1.2"},
			wantErr: "key_file is required",
		},
		{
			name:    "invalid min version",
			cfg:     TLSConfig{Enabled: true, CertFile: certFile, KeyFile: keyFile, MinVersion: "1.1"},
			wantErr: "invalid tls min_version",
		},
		{
			name:    "unreadable key pair",
			cfg:     TLSConfig{Enabled: true, CertFile: badFile, KeyFile: badFile, MinVersion: "1.2"},
			wantErr: "load tls key pair",
		},
		{
			name: "mutual tls without ca file",
			cfg: TLSConfig{
				Enabled: true, CertFile: certFile, KeyFile: keyFile,
				MinVersion: "1.2", MutualTLS: true,
			},
			wantErr: "client_ca_file is required",
		},
		{
			name: "missing ca file",
			cfg: TLSConfig{
				Enabled: true, CertFile: certFile, KeyFile: keyFile,
				MinVersion: "1.2", MutualTLS: true,
				ClientCAFile: filepath.Join(t.TempDir(), "missing.crt"),
			},
			wantErr: "read tls client_ca_file",
		},
		{
			name: "invalid ca file",
			cfg: TLSConfig{
				Enabled: true, CertFile: certFile, KeyFile: keyFile,
				MinVersion: "1.2", MutualTLS: true, ClientCAFile: badFile,
			},
			wantErr: "no valid CA certificates",
		},
		{
			name: "empty ca file",
			cfg: TLSConfig{
				Enabled: true, CertFile: certFile, KeyFile: keyFile,
				MinVersion: "1.2", MutualTLS: true,
				ClientCAFile: writeTestFile(t, t.TempDir(), "empty.crt", caPEM[:0]),
			},
			wantErr: "no valid CA certificates",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := BuildTLSConfig(tc.cfg)
			if err == nil {
				t.Fatalf("BuildTLSConfig() = %+v, want an error", cfg)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
			}
			if cfg != nil {
				t.Fatalf("invalid config should not return a TLS config, got %+v", cfg)
			}
		})
	}
}
