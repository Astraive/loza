package main

import (
	"context"
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

	stor, err := storage.NewStorage(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize storage")
	}
	defer stor.Close()

	log.Info().Msg("Storage initialized")

	processCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	syncProcessor := processor.NewEventProcessor(stor.Events(), stor.Topology(), stor.Graph())
	syncDone := startCollectorSync(processCtx, cfg.Collector, syncProcessor)
	if cfg.Collector.SourceOfTruth && cfg.Collector.Mode == "pull" {
		log.Info().
			Str("collector_url", cfg.Collector.URL).
			Bool("tail_enabled", cfg.Collector.TailEnabled).
			Msg("Collector source-of-truth sync started")
	}

	var grpcReady <-chan *grpc.Server
	if cfg.GRPC.Enabled {
		grpcReady = startGRPCServer(cfg, stor)
	}

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Info().Str("addr", addr).Msg("Starting HTTP server")

	cortexServer := httpserver.New(cfg, stor)
	defer cortexServer.Stop()

	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      cortexServer.Router(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("Server failed")
		}
	}()

	<-processCtx.Done()
	log.Info().Msg("Shutting down server...")

	shutdownGRPCServer(grpcReady)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("Server shutdown error")
	}

	<-syncDone
	log.Info().Msg("Collector source-of-truth sync stopped")
	log.Info().Msg("Server shutdown complete")
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

// startGRPCServer starts the gRPC server in a goroutine and returns a channel
// that hands the constructed *grpc.Server back to the caller. The channel is
// closed without a value if setup fails (e.g. TLS or listener setup errors), so
// a caller waiting for shutdown never blocks on a server that will not arrive.
func startGRPCServer(cfg *config.Config, stor storage.Storage) <-chan *grpc.Server {
	ready := make(chan *grpc.Server, 1)
	go func() {
		defer close(ready)

		grpcAddr := fmt.Sprintf("%s:%d", cfg.GRPC.Host, cfg.GRPC.Port)
		log.Info().Str("addr", grpcAddr).Msg("Starting gRPC server")

		var opts []grpc.ServerOption
		if cfg.TLS.Enabled {
			creds, err := credentials.NewServerTLSFromFile(cfg.TLS.CertFile, cfg.TLS.KeyFile)
			if err != nil {
				log.Error().Err(err).Msg("Failed to load TLS certs")
				return
			}
			opts = append(opts, grpc.Creds(creds))
		}

		listener, err := net.Listen("tcp", grpcAddr)
		if err != nil {
			log.Error().Err(err).Msg("Failed to create listener")
			return
		}

		server := grpc.NewServer(opts...)
		grpcSvc := grpcserver.New(cfg, stor)
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
