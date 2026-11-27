package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	lozacortex "github.com/astraive/loza/cortex"
	"github.com/astraive/loza/cortex/internal/collectorsync"
	"github.com/astraive/loza/cortex/internal/config"
	"github.com/astraive/loza/cortex/internal/graph"
	"github.com/astraive/loza/cortex/internal/logging"
	"github.com/astraive/loza/cortex/internal/processor"
	"github.com/astraive/loza/cortex/internal/storage"
	grpcserver "github.com/astraive/loza/cortex/server/grpc"
	httpserver "github.com/astraive/loza/cortex/server/http"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var version = lozacortex.Version

const (
	// grpcReadyTimeout bounds how long shutdown waits for startGRPCServer to hand
	// off the *grpc.Server when a signal arrives before it finishes starting.
	grpcReadyTimeout = 10 * time.Second
	// grpcStopTimeout bounds a graceful stop before forcing Stop.
	grpcStopTimeout = 10 * time.Second
)

var (
	configPath = flag.String("config", "", "Path to configuration file")
	logLevel   = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logFormat  = flag.String("log-format", "json", "Log format (json, console)")
)

func main() {
	flag.Parse()

	logger := logging.New(*logLevel, *logFormat)
	log.Logger = logger.GetZerologLogger()

	log.Info().Msg("Starting LOZA Cortex")

	var (
		cfg *config.Config
		err error
	)
	if *configPath == "" {
		cfg, err = config.LoadDefault()
	} else {
		cfg, err = config.Load(*configPath)
	}
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load config")
	}
	if *configPath != "" {
		log.Info().Str("config", *configPath).Msg("Loaded configuration")
	}

	// Build the shared TLS config once, before any listener starts. An invalid
	// TLS setup must fail startup instead of silently serving plaintext.
	tlsCfg, err := config.BuildTLSConfig(cfg.TLS)
	if err != nil {
		log.Fatal().Err(err).Msg("Invalid TLS configuration")
	}

	stor, err := storage.NewStorage(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize storage")
	}
	defer stor.Close()

	log.Info().Msg("Storage initialized")

	processCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	graphStore := graph.NewVersionedGraphStore(stor.Graph())
	syncProcessor := newSyncProcessor(cfg, stor, graphStore)
	// Single server construction path: build the Cortex server once, start its
	// correlation analyzer under the managed process context, and reuse its
	// router for the HTTP server below.
	cortexSrv := httpserver.New(cfg, stor, graphStore)
	defer cortexSrv.Stop()
	analyzerDone := cortexSrv.StartAnalyzer(processCtx)

	syncDone := startCollectorSync(processCtx, cfg.Collector, syncProcessor)
	if cfg.Collector.SourceOfTruth && cfg.Collector.Mode == "pull" {
		log.Info().
			Str("collector_url", cfg.Collector.URL).
			Bool("tail_enabled", cfg.Collector.TailEnabled).
			Msg("Collector source-of-truth sync started")
	}

	var grpcReady <-chan *grpc.Server
	if cfg.GRPC.Enabled {
		grpcReady = startGRPCServer(cfg, stor, graphStore)
	}

	log.Info().Str("addr", httpAddr(cfg)).Msg("Starting HTTP server")
	httpSrv := newHTTPServer(cfg, tlsCfg, cortexSrv.Router())

	listenErr := make(chan error, 1)
	go runHTTPServer(httpSrv, listenErr)

	select {
	case err := <-listenErr:
		// A listen failure must not terminate the process: cancel the shared
		// context so the collector sync unwinds and the shutdown sequence
		// below (gRPC graceful stop, HTTP shutdown, deferred stor.Close) runs.
		log.Error().Err(err).Msg("HTTP server failed, initiating shutdown")
		stopSignals()
	case <-processCtx.Done():
		log.Info().Msg("Shutting down server...")
	}

	shutdownGRPCServer(grpcReady)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("Server shutdown error")
	}

	<-analyzerDone
	log.Info().Msg("Correlation analyzer stopped")

	<-syncDone
	log.Info().Msg("Collector source-of-truth sync stopped")
	log.Info().Msg("Server shutdown complete")
}

// runHTTPServer serves until the server is shut down. Any unexpected listen
// error is reported to errCh so the caller can perform an orderly shutdown;
// it must never call log.Fatal, which would exit the process and skip main's
// deferred cleanup.
func runHTTPServer(httpSrv *http.Server, errCh chan<- error) {
	var err error
	if httpSrv.TLSConfig != nil {
		// The certificate chain is preloaded into TLSConfig.BuildTLSConfig, so
		// empty file names make ServeTLS use those in-memory certificates.
		err = httpSrv.ListenAndServeTLS("", "")
	} else {
		err = httpSrv.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		errCh <- err
	}
}

// newSyncProcessor builds the EventProcessor used by the collector source-of-truth
// sync. It routes through the shared factory so PII redaction is applied on the
// sync ingestion path just like HTTP, gRPC and GraphQL.
func newSyncProcessor(cfg *config.Config, stor storage.Storage, graphStore storage.GraphStore) *processor.EventProcessor {
	return processor.NewConfiguredEventProcessor(stor.Events(), stor.Topology(), graphStore, cfg.PIIRedaction)
}

func startCollectorSync(ctx context.Context, cfg config.CollectorConfig, proc collectorsync.BatchProcessor) <-chan struct{} {
	done := make(chan struct{})
	if !cfg.SourceOfTruth || cfg.Mode != "pull" {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		collectorsync.RunSourceOfTruthSync(ctx, cfg, proc)
	}()
	return done
}

// httpAddr returns the configured HTTP listen address.
func httpAddr(cfg *config.Config) string {
	return fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
}

// newHTTPServer builds the HTTP server and attaches the shared TLS config so
// that ListenAndServeTLS enforces MinVersion and mTLS. A nil tlsCfg means TLS
// is disabled and the caller serves plaintext. The config is cloned because
// net/http mutates NextProtos when it enables HTTP/2.
func newHTTPServer(cfg *config.Config, tlsCfg *tls.Config, handler http.Handler) *http.Server {
	var serverTLS *tls.Config
	if tlsCfg != nil {
		serverTLS = tlsCfg.Clone()
	}
	return &http.Server{
		Addr:         httpAddr(cfg),
		Handler:      handler,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		TLSConfig:    serverTLS,
	}
}

// grpcServerOptions derives the gRPC server options from the same TLS config
// used by HTTP, so MinVersion and mTLS apply to both transports.
func grpcServerOptions(cfg *config.Config, tlsCfg *tls.Config) ([]grpc.ServerOption, error) {
	if !cfg.TLS.Enabled {
		return nil, nil
	}
	if tlsCfg == nil {
		var err error
		tlsCfg, err = config.BuildTLSConfig(cfg.TLS)
		if err != nil {
			return nil, err
		}
	}
	return []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg.Clone()))}, nil
}

// startGRPCServer starts the gRPC server in a goroutine and returns a channel
// that hands the constructed *grpc.Server back to the caller. The channel is
// closed without a value if setup fails (e.g. TLS or listener setup errors), so
// a caller waiting for shutdown never blocks on a server that will not arrive.
func startGRPCServer(cfg *config.Config, stor storage.Storage, graphStores ...storage.GraphStore) <-chan *grpc.Server {
	ready := make(chan *grpc.Server, 1)
	go func() {
		defer close(ready)

		grpcAddr := fmt.Sprintf("%s:%d", cfg.GRPC.Host, cfg.GRPC.Port)
		log.Info().Str("addr", grpcAddr).Msg("Starting gRPC server")

		opts, err := grpcServerOptions(cfg, nil)
		if err != nil {
			log.Error().Err(err).Msg("Invalid TLS configuration for gRPC")
			return
		}
		// Enforce the same auth, role and rate-limit policy as the HTTP API on
		// every gRPC RPC, including streams.
		opts = append(opts, grpcserver.ServerOptions(cfg)...)

		listener, err := net.Listen("tcp", grpcAddr)
		if err != nil {
			log.Error().Err(err).Msg("Failed to create listener")
			return
		}

		server := grpc.NewServer(opts...)
		grpcSvc := grpcserver.New(cfg, stor, graphStores...)
		grpcSvc.RegisterServer(server)

		// Hand off before Serve blocks so shutdown can always reach the server,
		// even when a signal arrives while it is still starting.
		ready <- server

		log.Info().Str("addr", grpcAddr).Msg("gRPC server ready")
		if err := server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			log.Error().Err(err).Msg("gRPC server failed")
		}
	}()
	return ready
}

// shutdownGRPCServer waits for the server handed off by startGRPCServer and
// stops it gracefully, forcing a Stop only if the graceful stop times out.
// A nil channel means gRPC is disabled and is a no-op.
func shutdownGRPCServer(ready <-chan *grpc.Server) {
	if ready == nil {
		return
	}

	var server *grpc.Server
	select {
	case s, ok := <-ready:
		if !ok || s == nil {
			return
		}
		server = s
	case <-time.After(grpcReadyTimeout):
		log.Warn().Msg("Timed out waiting for gRPC server to start; skipping gRPC shutdown")
		return
	}

	stopped := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
		log.Info().Msg("gRPC server stopped")
	case <-time.After(grpcStopTimeout):
		log.Warn().Msg("gRPC graceful stop timed out, forcing")
		server.Stop()
	}
}
