package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCheckHealthContextHonorsCanceledContext(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := CheckHealthContext(ctx, srv.URL)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context error, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("expected no request to be sent with a canceled context, got %d hits", hits.Load())
	}
}

func TestFetchStatusContextHonorsCanceledContext(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := FetchStatusContext(ctx, srv.URL)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context error, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("expected no request to be sent with a canceled context, got %d hits", hits.Load())
	}
}

func TestFetchCortexMetricsContextHonorsCanceledContext(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("loza_cortex_events_ingested_total 1\n"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := FetchCortexMetricsContext(ctx, srv.URL)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context error, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("expected no request to be sent with a canceled context, got %d hits", hits.Load())
	}
}
