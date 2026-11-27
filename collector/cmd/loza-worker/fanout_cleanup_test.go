package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	collectorevent "github.com/astraive/loza/collector/internal/event"
)

type workerFanoutCleanupTestSink struct {
	closeCalls int
}

func (*workerFanoutCleanupTestSink) Name() string { return "fanout-cleanup-test" }

func (*workerFanoutCleanupTestSink) WriteEvent(context.Context, []byte, *collectorevent.Event) error {
	return nil
}

func (*workerFanoutCleanupTestSink) Flush(context.Context) error { return nil }

func (s *workerFanoutCleanupTestSink) Close(context.Context) error {
	s.closeCalls++
	return nil
}

func TestCreateFanoutSinksClosesEarlierSinksOnUnsupportedType(t *testing.T) {
	secondarySink := &workerFanoutCleanupTestSink{}
	fallbackSink := &workerFanoutCleanupTestSink{}
	cfg := workerConfig{fanoutOutputs: []workerFanoutOutput{
		{name: "secondary", role: fanoutRoleSecondary, sinkType: fanoutSinkLoki, enabled: true},
		{name: "fallback", role: fanoutRoleFallback, sinkType: fanoutSinkOTLP, enabled: true},
		{name: "unsupported", role: fanoutRoleSecondary, sinkType: "unknown", enabled: true},
	}}
	factories := workerFanoutSinkFactories{
		loki: func(workerFanoutOutput) (collectorevent.Sink, error) {
			return secondarySink, nil
		},
		otlp: func(workerFanoutOutput) (collectorevent.Sink, error) {
			return fallbackSink, nil
		},
	}

	secondary, fallback, dbs, err := createFanoutSinksWithFactories(cfg, factories)
	if err == nil {
		t.Fatal("expected unsupported sink type error")
	}
	if secondary != nil || fallback != nil || dbs != nil {
		t.Fatalf("expected no resources on failure, got secondary=%v fallback=%v dbs=%v", secondary, fallback, dbs)
	}
	if secondarySink.closeCalls != 1 || fallbackSink.closeCalls != 1 {
		t.Fatalf("close calls = secondary:%d fallback:%d, want 1 each", secondarySink.closeCalls, fallbackSink.closeCalls)
	}
}

func TestCreateFanoutSinksClosesSinksAndDatabasesOnConstructorError(t *testing.T) {
	firstSink := &workerFanoutCleanupTestSink{}
	failedSink := &workerFanoutCleanupTestSink{}
	firstDB := newRecordingDB(t)
	failedDB := newRecordingDB(t)
	if err := firstDB.Ping(); err != nil {
		t.Fatalf("open first database connection: %v", err)
	}
	if err := failedDB.Ping(); err != nil {
		t.Fatalf("open failing database connection: %v", err)
	}

	cfg := workerConfig{fanoutOutputs: []workerFanoutOutput{
		{name: "first", role: fanoutRoleSecondary, sinkType: fanoutSinkDuckDB, enabled: true},
		{name: "second", role: fanoutRoleSecondary, sinkType: fanoutSinkDuckDB, enabled: true},
	}}
	factories := workerFanoutSinkFactories{
		duckDB: func(_ workerConfig, output workerFanoutOutput) (collectorevent.Sink, *sql.DB, error) {
			if output.name == "first" {
				return firstSink, firstDB, nil
			}
			return failedSink, failedDB, errors.New("simulated constructor failure")
		},
	}

	_, _, _, err := createFanoutSinksWithFactories(cfg, factories)
	if err == nil {
		t.Fatal("expected constructor error")
	}
	if firstSink.closeCalls != 1 || failedSink.closeCalls != 1 {
		t.Fatalf("close calls = first:%d failed:%d, want 1 each", firstSink.closeCalls, failedSink.closeCalls)
	}
	if err := firstDB.Ping(); err == nil {
		t.Fatal("expected first database to be closed after constructor failure")
	}
	if err := failedDB.Ping(); err == nil {
		t.Fatal("expected database returned with constructor error to be closed")
	}
}

func TestCreateFanoutSinksTransfersOwnershipOnSuccess(t *testing.T) {
	sink := &workerFanoutCleanupTestSink{}
	cfg := workerConfig{fanoutOutputs: []workerFanoutOutput{
		{name: "secondary", role: fanoutRoleSecondary, sinkType: fanoutSinkLoki, enabled: true},
	}}
	factories := workerFanoutSinkFactories{
		loki: func(workerFanoutOutput) (collectorevent.Sink, error) {
			return sink, nil
		},
	}

	secondary, fallback, dbs, err := createFanoutSinksWithFactories(cfg, factories)
	if err != nil {
		t.Fatalf("create fanout sinks: %v", err)
	}
	if len(secondary) != 1 || secondary[0].Sink != sink || fallback != nil || len(dbs) != 0 {
		t.Fatalf("unexpected successful resources: secondary=%v fallback=%v dbs=%v", secondary, fallback, dbs)
	}
	if sink.closeCalls != 0 {
		t.Fatalf("sink was closed before ownership transfer: close calls=%d", sink.closeCalls)
	}
	if err := secondary[0].Sink.Close(context.Background()); err != nil {
		t.Fatalf("caller close: %v", err)
	}
	if sink.closeCalls != 1 {
		t.Fatalf("caller close calls=%d, want 1", sink.closeCalls)
	}
}
