package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
)

func configureHTTPServerTLS(server *http.Server, cfg collectorConfig) error {
	if !cfg.serverConfig.HTTP.TLSEnabled {
		return nil
	}
	verifyClientCert := cfg.serverConfig.HTTP.TLSRequireClientCert || cfg.identityMode == "mtls"
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	if cfg.serverConfig.HTTP.TLSClientCAFile != "" {
		caBytes, err := os.ReadFile(cfg.serverConfig.HTTP.TLSClientCAFile)
		if err != nil {
			return fmt.Errorf("read http client ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caBytes) {
			return fmt.Errorf("parse http client ca")
		}
		tlsConfig.ClientCAs = pool
	}
	if verifyClientCert {
		// crypto/tls falls back to the host's system root pool when ClientCAs is
		// nil. That would accept any publicly trusted client certificate and
		// silently defeat mTLS identity restrictions, so fail closed instead.
		if tlsConfig.ClientCAs == nil {
			return fmt.Errorf("http client certificate verification requires a client CA file")
		}
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	} else if tlsConfig.ClientCAs != nil {
		tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
	}
	server.TLSConfig = tlsConfig
	return nil
}
