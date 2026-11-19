package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// BuildTLSConfig constructs the server-side TLS configuration shared by the
// HTTP and gRPC listeners. It returns (nil, nil) when TLS is disabled, and a
// descriptive error when TLS is enabled but the configuration is incomplete or
// invalid, so callers never silently fall back to plaintext.
//
// The returned config carries the already-loaded certificate chain, the
// configured minimum protocol version and, when mutual TLS is enabled, the
// client CA pool with client certificate verification.
// BuildTLSConfig builds a *tls.Config from the supplied TLS settings. It
// returns (nil, nil) when TLS is disabled so callers can keep serving
// plaintext in explicitly local environments. When TLS is enabled it
// validates the settings, loads the server key pair, enforces the configured
// minimum version, and, for mutual TLS, loads the client CA bundle and
// requires verified client certificates. Any invalid setting is returned as
// an error so callers can fail fast instead of silently falling back to
// plaintext.
func BuildTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	if cfg.CertFile == "" {
		return nil, fmt.Errorf("tls cert_file is required when tls is enabled")
	}
	if cfg.KeyFile == "" {
		return nil, fmt.Errorf("tls key_file is required when tls is enabled")
	}

	minVersion, err := tlsMinVersion(cfg.MinVersion)
	if err != nil {
		return nil, err
	}

	if cfg.CertFile == "" || cfg.KeyFile == "" {
		return nil, fmt.Errorf("tls: cert_file and key_file are required when tls is enabled")
	}
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("tls: load cert_file/key_file: %w", err)
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load tls key pair: %w", err)
	}

	tlsCfg := &tls.Config{
		MinVersion:   minVersion,
		Certificates: []tls.Certificate{cert},
	}

	if cfg.MutualTLS {
		if cfg.ClientCAFile == "" {
			return nil, fmt.Errorf("tls: client_ca_file is required when mutual_tls is enabled")
		}
		caPEM, err := os.ReadFile(cfg.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("tls: read client_ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("tls: client_ca_file %q contains no valid CA certificates", cfg.ClientCAFile)
			return nil, fmt.Errorf("tls client_ca_file is required when mutual_tls is enabled")
		}
		caPEM, err := os.ReadFile(cfg.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("read tls client_ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("tls client_ca_file contains no valid CA certificates")
		}
		tlsCfg.ClientCAs = pool
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return tlsCfg, nil
}

func tlsMinVersion(v string) (uint16, error) {
	switch v {
// tlsMinVersion maps the configured min_version string to a tls version
// constant. An empty value is treated as 1.2 for backward compatibility with
// configurations that predate the min_version field.
func tlsMinVersion(version string) (uint16, error) {
	switch version {
	case "", "1.2":
		return tls.VersionTLS12, nil
	case "1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("tls: invalid min_version %q (must be \"1.2\" or \"1.3\")", v)
		return 0, fmt.Errorf("invalid tls min_version: %s (must be '1.2' or '1.3')", version)
	}
}
