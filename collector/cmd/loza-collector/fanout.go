package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	collectorevent "github.com/astraive/loza/collector/internal/event"
	processing "github.com/astraive/loza/collector/internal/processing"
	"github.com/astraive/loza/collector/internal/sinks/clickhouse"
	"github.com/astraive/loza/collector/internal/sinks/duckdb"
	"github.com/astraive/loza/collector/internal/sinks/gcs"
	"github.com/astraive/loza/collector/internal/sinks/loki"
	"github.com/astraive/loza/collector/internal/sinks/otlp"
	"github.com/astraive/loza/collector/internal/sinks/postgres"
	"github.com/astraive/loza/collector/internal/sinks/s3"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	s3svc "github.com/aws/aws-sdk-go-v2/service/s3"
	"google.golang.org/api/option"
)

const (
	deliveryPolicyBestEffort     = "best_effort"
	deliveryPolicyRequirePrimary = "require_primary"
	deliveryPolicyRequireAll     = "require_all"
	fanoutRoleSecondary          = "secondary"
	fanoutRoleFallback           = "fallback"
	fanoutSinkDuckDB             = "duckdb"
	fanoutSinkLoki               = "loki"
	fanoutSinkOTLP               = "otlp"
	fanoutSinkClickHouse         = "clickhouse"
	fanoutSinkPostgres           = "postgres"
	fanoutSinkS3                 = "s3"
	fanoutSinkGCS                = "gcs"
)

type collectorFanoutOutput struct {
	name            string
	role            string
	sinkType        string
	connection      string
	enabled         bool
	duckDBPath      string
	duckDBTable     string
	duckDBRawColumn string
	duckDBStoreRaw  *bool
	lokiURL         string
	lokiTenantID    string
	lokiLabels      map[string]string
	lokiBatchSize   int
	lokiFlushIntvl  time.Duration
	lokiTimeout     time.Duration
	otlpEndpoint    string
	otlpInsecure    bool
	otlpProtocol    string
	otlpTimeout     time.Duration
	otlpLogger      string
	chAddrs         []string
	chDatabase      string
	chUsername      string
	chPassword      string
	chTable         string
	chSchema        map[string]string
	chStoreRaw      bool
	chRawColumn     string
	pgDSN           string
	pgTable         string
	pgSchema        map[string]string
	pgStoreRaw      bool
	pgRawColumn     string
	s3Bucket        string
	s3Prefix        string
	s3Region        string
	s3Endpoint      string
	s3AccessKey     string
	s3SecretKey     string
	s3Schema        map[string]string
	s3BatchSize     int
	s3FlushIntvl    time.Duration
	gcsBucket       string
	gcsPrefix       string
	gcsCredsFile    string
	gcsSchema       map[string]string
	gcsBatchSize    int
	gcsFlushIntvl   time.Duration
}

type namedSink = processing.NamedSink

// validateFanoutRoles rejects configurations with more than one enabled
// fallback output. Without this check, createFanoutSinks would create a sink
// for every fallback and then silently keep only the last one, leaving the
// overwritten sinks (and their background flush loops) running but
// unreachable and never closed. Config validation normally catches this, but
// guarding here keeps the invariant local to sink creation as well.
func validateFanoutRoles(outputs []collectorFanoutOutput) error {
	fallbacks := 0
	for _, output := range outputs {
		if output.enabled && output.role == fanoutRoleFallback {
			fallbacks++
		}
	}
	if fallbacks > 1 {
		return fmt.Errorf("fanout supports at most one enabled fallback output, found %d", fallbacks)
	}
	return nil
}

// fanoutSinkFactory builds the sink for a single fanout output. It is a
// variable so regression tests can substitute a deterministic stub; in
// production it dispatches to the concrete sink constructors below.
var fanoutSinkFactory = func(cfg collectorConfig, output collectorFanoutOutput, sinkType string) (collectorevent.Sink, *sql.DB, error) {
	switch sinkType {
	case fanoutSinkDuckDB:
		return newDuckDBFanoutSink(cfg, output)
	case fanoutSinkLoki:
		sink, err := newLokiFanoutSink(output)
		return sink, nil, err
	case fanoutSinkOTLP:
		sink, err := newOTLPFanoutSink(output)
		return sink, nil, err
	case fanoutSinkClickHouse:
		sink, err := newClickHouseFanoutSink(cfg, output)
		return sink, nil, err
	case fanoutSinkPostgres:
		sink, err := newPostgresFanoutSink(cfg, output)
		return sink, nil, err
	case fanoutSinkS3:
		sink, err := newS3FanoutSink(cfg, output)
		return sink, nil, err
	case fanoutSinkGCS:
		sink, err := newGCSFanoutSink(cfg, output)
		return sink, nil, err
	default:
		return nil, nil, fmt.Errorf("fanout output %q: unsupported type %q", output.name, sinkType)
	}
}

// closeFanoutSinks releases every resource acquired while building fanout
// sinks. Sinks own background work (flush loops and clients such as GCS's
// storage.Client), while DuckDB handles may be caller-owned, so both are
// closed. Closing an already-closed sink or handle is a safe no-op.
func closeFanoutSinks(sinks []collectorevent.Sink, dbs []*sql.DB) {
	for _, sink := range sinks {
		if sink != nil {
			_ = sink.Close(context.Background())
		}
	}
	for _, fanoutDB := range dbs {
		if fanoutDB != nil {
			_ = fanoutDB.Close()
		}
	}
}

func createFanoutSinks(cfg collectorConfig) ([]namedSink, *namedSink, []*sql.DB, error) {
	if err := validateFanoutRoles(cfg.fanoutOutputs); err != nil {
		return nil, nil, nil, err
	}

	secondary := make([]namedSink, 0, len(cfg.fanoutOutputs))
	var fallback *namedSink
	dbs := make([]*sql.DB, 0, len(cfg.fanoutOutputs))
	createdSinks := make([]collectorevent.Sink, 0, len(cfg.fanoutOutputs))

	for _, output := range cfg.fanoutOutputs {
		if !output.enabled {
			continue
		}

		var (
			sink collectorevent.Sink
			db   *sql.DB
			err  error
		)

		if output.connection != "" {
			if err := applyFanoutConnection(&output, cfg); err != nil {
				closeFanoutSinks(createdSinks, dbs)
				return nil, nil, nil, err
			}
		}
		sinkType := output.sinkType
		if sinkType == "" {
			sinkType = fanoutSinkDuckDB
		}

		sink, db, err = fanoutSinkFactory(cfg, output, sinkType)
		if sink != nil {
			createdSinks = append(createdSinks, sink)
		}
		if db != nil {
			dbs = append(dbs, db)
		}
		if err != nil {
			closeFanoutSinks(createdSinks, dbs)
			return nil, nil, nil, err
		}

		named := namedSink{Name: output.name, Sink: sink}
		if output.role == fanoutRoleFallback {
			fallback = &named
			continue
		}
		secondary = append(secondary, named)
	}

	return secondary, fallback, dbs, nil
}

func newDuckDBFanoutSink(cfg collectorConfig, output collectorFanoutOutput) (collectorevent.Sink, *sql.DB, error) {
	path := strings.TrimSpace(output.duckDBPath)
	if path == "" {
		return nil, nil, fmt.Errorf("fanout output %q: duckdb.path must not be empty", output.name)
	}
	table := strings.TrimSpace(output.duckDBTable)
	if table == "" {
		table = cfg.duckDBTable
	}
	rawColumn := strings.TrimSpace(output.duckDBRawColumn)
	if rawColumn == "" {
		rawColumn = cfg.duckDBRawColumn
	}
	storeRaw := cfg.duckDBStoreRaw
	if output.duckDBStoreRaw != nil {
		storeRaw = *output.duckDBStoreRaw
	}

	db, err := sql.Open(cfg.duckDBDriver, path)
	if err != nil {
		return nil, nil, fmt.Errorf("fanout output %q: open duckdb: %w", output.name, err)
	}
	db.SetMaxOpenConns(cfg.duckDBMaxOpenConns)
	db.SetMaxIdleConns(cfg.duckDBMaxIdleConns)

	schemaCfg := cfg
	schemaCfg.duckDBTable = table
	schemaCfg.duckDBRawColumn = rawColumn
	schemaCfg.duckDBStoreRaw = storeRaw
	if err := ensureSchema(db, schemaCfg); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("fanout output %q: ensure schema: %w", output.name, err)
	}

	sink, err := duckdb.New(duckdb.Config{
		DB:              db,
		Driver:          cfg.duckDBDriver,
		Table:           table,
		StoreRaw:        storeRaw,
		RawColumn:       rawColumn,
		Schema:          cfg.duckDBSchema,
		BatchSize:       cfg.duckDBBatchSize,
		FlushInterval:   cfg.duckDBFlushInterval,
		WriterLoop:      cfg.duckDBWriterLoop,
		WriterQueueSize: cfg.duckDBWriterQueueSize,
		EncryptRaw:      true,
		EncryptKey:      cfg.storageEncryptionKey,
	})
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("fanout output %q: create sink: %w", output.name, err)
	}
	return sink, db, nil
}

func newLokiFanoutSink(output collectorFanoutOutput) (collectorevent.Sink, error) {
	if strings.TrimSpace(output.lokiURL) == "" {
		return nil, fmt.Errorf("fanout output %q: loki.url must not be empty", output.name)
	}
	return loki.New(loki.Config{
		URL:           output.lokiURL,
		TenantID:      output.lokiTenantID,
		Labels:        output.lokiLabels,
		BatchSize:     output.lokiBatchSize,
		FlushInterval: output.lokiFlushIntvl,
		Timeout:       output.lokiTimeout,
	})
}

func newOTLPFanoutSink(output collectorFanoutOutput) (collectorevent.Sink, error) {
	if strings.TrimSpace(output.otlpEndpoint) == "" {
		return nil, fmt.Errorf("fanout output %q: otlp.endpoint must not be empty", output.name)
	}
	return otlp.New(context.Background(), otlp.Config{
		Endpoint: output.otlpEndpoint,
		Insecure: output.otlpInsecure,
		Protocol: output.otlpProtocol,
		Timeout:  output.otlpTimeout,
		Logger:   output.otlpLogger,
	})
}

func newClickHouseFanoutSink(cfg collectorConfig, output collectorFanoutOutput) (collectorevent.Sink, error) {
	if len(output.chAddrs) == 0 {
		return nil, fmt.Errorf("fanout output %q: clickhouse.addrs must not be empty", output.name)
	}
	return clickhouse.New(clickhouse.Config{
		Addrs:      output.chAddrs,
		Database:   output.chDatabase,
		Username:   output.chUsername,
		Password:   output.chPassword,
		Table:      output.chTable,
		Schema:     output.chSchema,
		StoreRaw:   output.chStoreRaw,
		RawColumn:  output.chRawColumn,
		EncryptRaw: true,
		EncryptKey: cfg.storageEncryptionKey,
	})
}

func newPostgresFanoutSink(cfg collectorConfig, output collectorFanoutOutput) (collectorevent.Sink, error) {
	if strings.TrimSpace(output.pgDSN) == "" {
		return nil, fmt.Errorf("fanout output %q: postgres.dsn must not be empty", output.name)
	}
	return postgres.New(context.Background(), postgres.Config{
		DSN:        output.pgDSN,
		Table:      output.pgTable,
		Schema:     output.pgSchema,
		StoreRaw:   output.pgStoreRaw,
		RawColumn:  output.pgRawColumn,
		EncryptRaw: true,
		EncryptKey: cfg.storageEncryptionKey,
	})
}

func newS3FanoutSink(cfg collectorConfig, output collectorFanoutOutput) (collectorevent.Sink, error) {
	if output.s3Bucket == "" {
		return nil, fmt.Errorf("fanout output %q: s3.bucket must not be empty", output.name)
	}

	cfgOpts := []func(*config.LoadOptions) error{}
	if output.s3Region != "" {
		cfgOpts = append(cfgOpts, config.WithRegion(output.s3Region))
	}
	if output.s3AccessKey != "" && output.s3SecretKey != "" {
		cfgOpts = append(cfgOpts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(output.s3AccessKey, output.s3SecretKey, "")))
	}

	awsCfg, err := config.LoadDefaultConfig(context.Background(), cfgOpts...)
	if err != nil {
		return nil, fmt.Errorf("fanout output %q: load aws config: %w", output.name, err)
	}

	s3Client := s3svc.NewFromConfig(awsCfg, func(o *s3svc.Options) {
		if output.s3Endpoint != "" {
			o.BaseEndpoint = &output.s3Endpoint
		}
	})

	return s3.New(s3.Config{
		Client:        s3Client,
		Bucket:        output.s3Bucket,
		Prefix:        output.s3Prefix,
		Schema:        output.s3Schema,
		BatchSize:     output.s3BatchSize,
		FlushInterval: output.s3FlushIntvl,
		EncryptKey:    cfg.storageEncryptionKey,
	})
}

func newGCSFanoutSink(cfg collectorConfig, output collectorFanoutOutput) (collectorevent.Sink, error) {
	if output.gcsBucket == "" {
		return nil, fmt.Errorf("fanout output %q: gcs.bucket must not be empty", output.name)
	}

	ctx := context.Background()
	opts := []option.ClientOption{}
	if output.gcsCredsFile != "" {
		credsJSON, err := os.ReadFile(output.gcsCredsFile)
		if err != nil {
			return nil, fmt.Errorf("fanout output %q: read gcs creds: %w", output.name, err)
		}
		//nolint:staticcheck // file-based GCS auth
		opts = append(opts, option.WithCredentialsJSON(credsJSON))
	}

	gcsClient, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("fanout output %q: create gcs client: %w", output.name, err)
	}

	return gcs.New(gcs.Config{
		Client:        gcsClient,
		Bucket:        output.gcsBucket,
		Prefix:        output.gcsPrefix,
		Schema:        output.gcsSchema,
		BatchSize:     output.gcsBatchSize,
		FlushInterval: output.gcsFlushIntvl,
		EncryptKey:    cfg.storageEncryptionKey,
	})
}

func (s *collectorState) sinksForShutdown() []namedSink {
	sinks := []namedSink{{Name: "primary", Sink: s.ingestSink}}
	sinks = append(sinks, s.secondarySinks...)
	if s.fallbackSink != nil {
		sinks = append(sinks, *s.fallbackSink)
	}
	seen := make(map[string]struct{}, len(sinks))
	out := make([]namedSink, 0, len(sinks))
	for _, sink := range sinks {
		if sink.Sink == nil {
			continue
		}
		if _, ok := seen[sink.Name]; ok {
			continue
		}
		seen[sink.Name] = struct{}{}
		out = append(out, sink)
	}
	return out
}
