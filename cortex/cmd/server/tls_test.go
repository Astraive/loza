package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/config"
	"github.com/astraive/loza/cortex/internal/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// tlsTestFiles writes a CA, a server certificate/key and a client
// certificate/key to a temporary directory.
func tlsTestFiles(t *testing.T) (certFile, keyFile, caFile, clientCertFile, clientKeyFile string) {
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
	return write("server.pem", serverCert), write("server-key.pem", serverKey),
		write("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})),
		write("client.pem", clientCert), write("client-key.pem", clientKey)
}

func freePort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	lis.Close()
	return port
}

func testCACert(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
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

func testLeafCert(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, cn string, usage x509.ExtKeyUsage) ([]byte, []byte) {
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
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func testWriteFile(t *testing.T, dir, name string, contents []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func testTLSConfig(t *testing.T, minVersion string, mutualTLS bool) (config.TLSConfig, []byte) {
	cfg, caPEM, _, _ := testTLSConfigWithCA(t, minVersion, mutualTLS)
	return cfg, caPEM
}

func testTLSConfigWithCA(t *testing.T, minVersion string, mutualTLS bool) (config.TLSConfig, []byte, *x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	dir := t.TempDir()
	ca, caKey, caPEM := testCACert(t)
	certPEM, keyPEM := testLeafCert(t, ca, caKey, "localhost", x509.ExtKeyUsageServerAuth)
	cfg := config.TLSConfig{
		Enabled:    true,
		CertFile:   testWriteFile(t, dir, "server.crt", certPEM),
		KeyFile:    testWriteFile(t, dir, "server.key", keyPEM),
		MinVersion: minVersion,
		MutualTLS:  mutualTLS,
	}
	if mutualTLS {
		cfg.ClientCAFile = testWriteFile(t, dir, "ca.crt", caPEM)
	}
	return cfg, caPEM, ca, caKey
}

func startTLSServer(t *testing.T, cfg config.Config, tlsCfg *tls.Config, handler http.Handler) string {
	t.Helper()
	srv := newHTTPServer(&cfg, tlsCfg, handler)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// The certificate chain is preloaded in tlsCfg, so ServeTLS uses it in memory.
	go func() { _ = srv.ServeTLS(lis, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })
	return lis.Addr().String()
}

func tlsTestCA(t *testing.T, caFile string) *x509.CertPool {
	t.Helper()
	pemData, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemData) {
		t.Fatal("failed to parse test CA")
	}
	return pool
}

// TestRunHTTPServerServesHTTPSWhenTLSConfigured is a regression test for the
// finding: before the fix runHTTPServer always used plaintext ListenAndServe,
// so an HTTPS client could not complete a handshake. After the fix the shared
// TLS config is attached and ListenAndServeTLS is used.
func TestRunHTTPServerServesHTTPSWhenTLSConfigured(t *testing.T) {
	certFile, keyFile, _, _, _ := tlsTestFiles(t)
	tlsCfg, err := config.BuildTLSConfig(config.TLSConfig{
		Enabled: true, CertFile: certFile, KeyFile: keyFile, MinVersion: "1.2",
	})
	if err != nil {
		t.Fatalf("BuildTLSConfig: %v", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	srv := &http.Server{
		Addr:      addr,
		TLSConfig: tlsCfg,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "secure")
		}),
	}
	errCh := make(chan error, 1)
	go runHTTPServer(srv, errCh)
	defer srv.Close()

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, // #nosec G402 -- test certificate
	}}
	var resp *http.Response
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err = client.Get("https://" + addr)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("HTTPS request failed (server may still be serving plaintext): %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "secure" {
		t.Fatalf("body = %q, want %q", body, "secure")
	}
	if resp.TLS == nil || resp.TLS.Version < tls.VersionTLS12 {
		t.Fatalf("connection did not negotiate TLS >= 1.2: %+v", resp.TLS)
	}
}

// TestRunHTTPServerHonorsConfiguredMinVersion proves the configured minimum is
// applied on the wire: a TLS 1.2-only client must be rejected when the server
// requires TLS 1.3.
func TestRunHTTPServerHonorsConfiguredMinVersion(t *testing.T) {
	certFile, keyFile, _, _, _ := tlsTestFiles(t)
	tlsCfg, err := config.BuildTLSConfig(config.TLSConfig{
		Enabled: true, CertFile: certFile, KeyFile: keyFile, MinVersion: "1.3",
	})
	if err != nil {
		t.Fatalf("BuildTLSConfig: %v", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	srv := &http.Server{Addr: addr, TLSConfig: tlsCfg}
	errCh := make(chan error, 1)
	go runHTTPServer(srv, errCh)
	defer srv.Close()

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // #nosec G402 -- test certificate
			MinVersion:         tls.VersionTLS12,
			MaxVersion:         tls.VersionTLS12,
		},
	}}
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := client.Get("https://" + addr)
		if err != nil {
			// TLS 1.2-only client rejected by the TLS 1.3 server, as expected.
			return
		}
		resp.Body.Close()
		if time.Now().After(deadline) {
			t.Fatal("TLS 1.2 client was accepted despite min_version: 1.3")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestStartGRPCServerUsesSharedTLSConfig verifies gRPC parity: the gRPC
// listener negotiates TLS with the configured version and enforces mutual TLS
// using the same shared TLS config as the HTTP listener.
func TestStartGRPCServerUsesSharedTLSConfig(t *testing.T) {
	certFile, keyFile, caFile, clientCertFile, clientKeyFile := tlsTestFiles(t)
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	cfg.GRPC.Enabled = true
	cfg.GRPC.Host = "127.0.0.1"
	cfg.GRPC.Port = freePort(t)
	cfg.Storage.Backend = "duckdb"
	cfg.Storage.DuckDB.Path = filepath.Join(t.TempDir(), "cortex.duckdb")
	cfg.TLS = config.TLSConfig{
		Enabled: true, CertFile: certFile, KeyFile: keyFile,
		MinVersion: "1.3", MutualTLS: true, ClientCAFile: caFile,
	}

	stor, err := storage.NewStorage(cfg)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	defer stor.Close()

	ready := startGRPCServer(cfg, stor)
	var server *grpc.Server
	select {
	case s, ok := <-ready:
		if !ok || s == nil {
			t.Fatal("gRPC server setup failed")
		}
		server = s
	case <-time.After(3 * time.Second):
		t.Fatal("gRPC server was not handed off")
	}
	defer server.Stop()

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.GRPC.Port)
	caPool := tlsTestCA(t, caFile)
	clientCert, err := tls.LoadX509KeyPair(clientCertFile, clientKeyFile)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the listener, then verify a TLS 1.3 handshake with a client
	// certificate succeeds and actually negotiates TLS 1.3.
	var conn *tls.Conn
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", addr, &tls.Config{
			RootCAs: caPool, ServerName: "localhost", MinVersion: tls.VersionTLS13,
			Certificates: []tls.Certificate{clientCert},
		})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("TLS handshake with gRPC server failed: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	version := conn.ConnectionState().Version
	conn.Close()
	if version != tls.VersionTLS13 {
		t.Fatalf("gRPC negotiated TLS %x, want TLS 1.3", version)
	}

	// A client without a certificate must be rejected by mutual TLS.
	unauth, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", addr, &tls.Config{
		RootCAs: caPool, ServerName: "localhost", MinVersion: tls.VersionTLS13,
	})
	if err == nil {
		// In TLS 1.3 the client can finish its side of the handshake before the
		// server's client-certificate verification alert arrives; force I/O so
		// the rejection is observed.
		_ = unauth.SetReadDeadline(time.Now().Add(time.Second))
		_, readErr := unauth.Read(make([]byte, 1))
		err = readErr
	}
	unauth.Close()
	if err == nil {
		t.Fatal("gRPC accepted a client without a certificate despite mutual_tls")
	}
}

func httpsClient(t *testing.T, caPEM []byte, clientCert, clientKey []byte, maxVersion uint16) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("failed to parse test CA")
	}
	tlsCfg := &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12, MaxVersion: maxVersion}
	if len(clientCert) > 0 {
		cert, err := tls.X509KeyPair(clientCert, clientKey)
		if err != nil {
			t.Fatal(err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
		Timeout:   2 * time.Second,
	}
}

func TestNewHTTPServerAttachesSharedTLSConfig(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	plain := newHTTPServer(cfg, nil, nil)
	if plain.TLSConfig != nil {
		t.Fatal("plaintext server should not have a TLS config")
	}

	tlsSettings, _ := testTLSConfig(t, "1.3", false)
	cfg.TLS = tlsSettings
	tlsCfg, err := config.BuildTLSConfig(cfg.TLS)
	if err != nil {
		t.Fatal(err)
	}
	secure := newHTTPServer(cfg, tlsCfg, nil)
	if secure.TLSConfig == nil {
		t.Fatal("HTTP server did not attach a TLS config")
	}
	if secure.TLSConfig.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion = %x, want TLS 1.3", secure.TLSConfig.MinVersion)
	}
	if len(secure.TLSConfig.Certificates) != 1 {
		t.Fatal("HTTP server TLS config is missing the server key pair")
	}
	if tlsCfg.MinVersion != tls.VersionTLS13 {
		t.Fatal("newHTTPServer mutated the shared TLS config")
	}
}

func TestHTTPSServerEnforcesMinimumVersion(t *testing.T) {
	for _, tc := range []struct {
		name       string
		minVersion string
	}{
		{name: "tls12", minVersion: "1.2"},
		{name: "tls13", minVersion: "1.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.Default()
			if err != nil {
				t.Fatalf("Default: %v", err)
			}
			var caPEM []byte
			cfg.TLS, caPEM = testTLSConfig(t, tc.minVersion, false)
			tlsCfg, err := config.BuildTLSConfig(cfg.TLS)
			if err != nil {
				t.Fatal(err)
			}
			addr := startTLSServer(t, *cfg, tlsCfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "ok")
			}))

			resp, err := httpsClient(t, caPEM, nil, nil, 0).Get("https://" + addr)
			if err != nil {
				t.Fatalf("HTTPS request failed: %v", err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != "ok" {
				t.Fatalf("response body = %q, want ok", body)
			}

			// Plaintext must not be served on the TLS listener. Go's HTTP
			// server answers a plaintext request with 400 rather than serving it.
			if plainResp, err := http.Get("http://" + addr); err == nil {
				plainResp.Body.Close()
				if plainResp.StatusCode == http.StatusOK {
					t.Fatal("plaintext request was served on the TLS listener")
				}
			}

			if tc.minVersion == "1.3" {
				// A client capped at TLS 1.2 must be rejected when 1.3 is required.
				if _, err := httpsClient(t, caPEM, nil, nil, tls.VersionTLS12).Get("https://" + addr); err == nil {
					t.Fatal("TLS 1.2 client was accepted with min_version 1.3")
				}
			}
		})
	}
}

func TestHTTPSServerMutualTLS(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	tlsCfgFromTest, caPEM, ca, caKey := testTLSConfigWithCA(t, "1.2", true)
	cfg.TLS = tlsCfgFromTest
	tlsCfg, err := config.BuildTLSConfig(cfg.TLS)
	if err != nil {
		t.Fatal(err)
	}
	addr := startTLSServer(t, *cfg, tlsCfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))

	if _, err := httpsClient(t, caPEM, nil, nil, 0).Get("https://" + addr); err == nil {
		t.Fatal("mTLS listener accepted a client without a certificate")
	}

	clientCert, clientKey := testLeafCert(t, ca, caKey, "loza-client", x509.ExtKeyUsageClientAuth)
	resp, err := httpsClient(t, caPEM, clientCert, clientKey, 0).Get("https://" + addr)
	if err != nil {
		t.Fatalf("mTLS request with a valid client certificate failed: %v", err)
	}
	defer resp.Body.Close()
}

func TestGRPCServerOptionsUseSharedTLSConfig(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	if opts, err := grpcServerOptions(cfg, nil); err != nil || len(opts) != 0 {
		t.Fatalf("disabled TLS: opts = %d, err = %v; want 0 options and no error", len(opts), err)
	}

	cfg.TLS, _ = testTLSConfig(t, "1.3", true)
	tlsCfg, err := config.BuildTLSConfig(cfg.TLS)
	if err != nil {
		t.Fatal(err)
	}
	opts, err := grpcServerOptions(cfg, tlsCfg)
	if err != nil {
		t.Fatalf("grpcServerOptions() error = %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("enabled TLS: got %d options, want 1", len(opts))
	}
	// credentials.NewTLS consumes the same config, including MinVersion/mTLS.
	creds := credentials.NewTLS(tlsCfg)
	if creds.Info().SecurityProtocol != "tls" {
		t.Fatalf("gRPC credentials protocol = %q, want tls", creds.Info().SecurityProtocol)
	}
	if tlsCfg.MinVersion != tls.VersionTLS13 || tlsCfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("gRPC TLS config lost settings: %+v", tlsCfg)
	}

	// gRPC must fail fast on an invalid config too, even without a shared config.
	cfg.TLS.CertFile = filepath.Join(t.TempDir(), "missing.crt")
	if _, err := grpcServerOptions(cfg, nil); err == nil || !strings.Contains(err.Error(), "load tls key pair") {
		t.Fatalf("invalid TLS config: err = %v, want key pair load error", err)
	}
}
