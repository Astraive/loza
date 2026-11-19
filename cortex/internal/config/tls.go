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
func BuildTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	if !cfg.Enabled {
		return nil, nil
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
		}
		tlsCfg.ClientCAs = pool
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return tlsCfg, nil
}

func tlsMinVersion(v string) (uint16, error) {
	switch v {
	case "", "1.2":
		return tls.VersionTLS12, nil
	case "1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("tls: invalid min_version %q (must be \"1.2\" or \"1.3\")", v)
	}
}
